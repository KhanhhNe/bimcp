param(
    [switch]$Force
)

$ErrorActionPreference = "Stop"

$project = Join-Path $PSScriptRoot "tom\TomBridge\TomBridge.csproj"
$bridgeRoot = Split-Path $project
$publish = Join-Path $bridgeRoot "bin\Release\net10.0\win-x64\publish"
$publishedDll = Join-Path $publish "tombridge.dll"
$output = Join-Path $PSScriptRoot "tom\bin"
$outputDll = Join-Path $output "tombridge.dll"
$generatorRoot = Join-Path $PSScriptRoot "tom\TomGen"
$generatorProject = Join-Path $generatorRoot "TomGen.csproj"
$generatedGo = Join-Path $PSScriptRoot "tom\generated.go"
$cachePath = Join-Path $PSScriptRoot "tom\obj\build-cache.json"
$vsInstaller = "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer"
$env:DOTNET_CLI_USE_MSBUILD_SERVER = "1"

if (Test-Path (Join-Path $vsInstaller "vswhere.exe")) {
    $env:PATH = "$vsInstaller;$env:PATH"
}

function Get-InputFingerprint {
    param(
        [string]$Root,
        [string[]]$Metadata
    )

    $lines = [System.Collections.Generic.List[string]]::new()
    foreach ($value in $Metadata) {
        $lines.Add($value)
    }

    Get-ChildItem $Root -File -Recurse |
        Where-Object { $_.FullName -notmatch '[\\/](bin|obj)[\\/]' } |
        Sort-Object FullName |
        ForEach-Object {
            $relativePath = $_.FullName.Substring($Root.TrimEnd('\').Length + 1).Replace('\', '/')
            $hash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash
            $lines.Add("$relativePath=$hash")
        }

    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes(($lines -join "`n"))
        return -join ($sha256.ComputeHash($bytes) | ForEach-Object { $_.ToString("x2") })
    }
    finally {
        $sha256.Dispose()
    }
}

function Get-RestoreFingerprint {
    param(
        [string]$Project,
        [string[]]$Metadata
    )

    $projectRoot = Split-Path $Project
    $restoreFiles = @(
        $Project
        (Join-Path $projectRoot "packages.lock.json")
        (Join-Path $projectRoot "NuGet.config")
        (Join-Path $PSScriptRoot "Directory.Build.props")
        (Join-Path $PSScriptRoot "Directory.Build.targets")
        (Join-Path $PSScriptRoot "Directory.Packages.props")
        (Join-Path $PSScriptRoot "global.json")
        (Join-Path $PSScriptRoot "NuGet.config")
        (Join-Path $env:APPDATA "NuGet\NuGet.Config")
    ) | Where-Object { Test-Path $_ } | Sort-Object -Unique

    $lines = [System.Collections.Generic.List[string]]::new()
    foreach ($value in $Metadata) {
        $lines.Add($value)
    }
    foreach ($file in $restoreFiles) {
        $hash = (Get-FileHash $file -Algorithm SHA256).Hash
        $lines.Add("$file=$hash")
    }

    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes(($lines -join "`n"))
        return -join ($sha256.ComputeHash($bytes) | ForEach-Object { $_.ToString("x2") })
    }
    finally {
        $sha256.Dispose()
    }
}

function Save-BuildCache {
    New-Item -ItemType Directory -Force (Split-Path $cachePath) | Out-Null
    $cache | ConvertTo-Json | Set-Content $cachePath
}

$cache = [ordered]@{}
if (Test-Path $cachePath) {
    try {
        $savedCache = Get-Content $cachePath -Raw | ConvertFrom-Json
        foreach ($property in $savedCache.PSObject.Properties) {
            $cache[$property.Name] = $property.Value
        }
    }
    catch {
        Write-Warning "Ignoring invalid build cache at $cachePath`: $($_.Exception.Message)"
    }
}

$dotnetVersion = dotnet --version
if ($LASTEXITCODE -ne 0) {
    throw "dotnet --version failed with exit code $LASTEXITCODE"
}

$bridgeAssets = Join-Path $bridgeRoot "obj\project.assets.json"
$bridgeRestoreFingerprint = Get-RestoreFingerprint $project @(
    "cache-version=1"
    "dotnet=$dotnetVersion"
    "runtime=win-x64"
)
if ($Force -or
    $cache["bridgeRestore"] -ne $bridgeRestoreFingerprint -or
    -not (Test-Path $bridgeAssets)) {
    Write-Host "Restoring native TOM bridge dependencies..."
    dotnet restore $project -r win-x64
    if ($LASTEXITCODE -ne 0) {
        throw "dotnet restore failed with exit code $LASTEXITCODE"
    }

    $cache["bridgeRestore"] = $bridgeRestoreFingerprint
    Save-BuildCache
}

$bridgeFingerprint = Get-InputFingerprint $bridgeRoot @(
    "cache-version=1"
    "dotnet=$dotnetVersion"
    "configuration=Release"
    "runtime=win-x64"
    "restore=$bridgeRestoreFingerprint"
)
$bridgeNeedsPublish = $Force -or
    $cache["bridge"] -ne $bridgeFingerprint -or
    (-not (Test-Path $publishedDll) -and -not (Test-Path $outputDll))

if ($bridgeNeedsPublish) {
    Write-Host "Publishing native TOM bridge..."
    dotnet publish $project -c Release --no-restore
    if ($LASTEXITCODE -ne 0) {
        throw "dotnet publish failed with exit code $LASTEXITCODE"
    }

    if (-not (Test-Path $publishedDll)) {
        throw "dotnet publish did not produce $publishedDll"
    }

    $cache["bridge"] = $bridgeFingerprint
    Save-BuildCache
}
else {
    Write-Host "TOM bridge inputs unchanged; using cached native DLL."
}

if (Test-Path $publishedDll) {
    New-Item -ItemType Directory -Force $output | Out-Null
    if (-not (Test-Path $outputDll) -or
        (Get-FileHash $publishedDll -Algorithm SHA256).Hash -ne
        (Get-FileHash $outputDll -Algorithm SHA256).Hash) {
        Copy-Item $publishedDll $outputDll -Force
    }
}
elseif (-not (Test-Path $outputDll)) {
    throw "Cached TOM bridge DLL is missing. Run .\build.ps1 -Force to rebuild it."
}

$generatorAssets = Join-Path $generatorRoot "obj\project.assets.json"
$generatorRestoreFingerprint = Get-RestoreFingerprint $generatorProject @(
    "cache-version=1"
    "dotnet=$dotnetVersion"
)
$generatorFingerprint = Get-InputFingerprint $generatorRoot @(
    "cache-version=1"
    "dotnet=$dotnetVersion"
    "configuration=Release"
    "restore=$generatorRestoreFingerprint"
)
$generatorNeedsRun = $Force -or
    $cache["generator"] -ne $generatorFingerprint -or
    -not (Test-Path $generatedGo)

if ($generatorNeedsRun) {
    if ($Force -or
        $cache["generatorRestore"] -ne $generatorRestoreFingerprint -or
        -not (Test-Path $generatorAssets)) {
        Write-Host "Restoring TOM generator dependencies..."
        dotnet restore $generatorProject
        if ($LASTEXITCODE -ne 0) {
            throw "dotnet restore failed with exit code $LASTEXITCODE"
        }

        $cache["generatorRestore"] = $generatorRestoreFingerprint
        Save-BuildCache
    }

    $gofmtCommand = Get-Command gofmt -ErrorAction SilentlyContinue
    $gofmtPath = if ($gofmtCommand) { $gofmtCommand.Source } else { $null }
    if (-not $gofmtPath) {
        $go = Get-Command go -ErrorAction SilentlyContinue
        if ($go) {
            $goRoot = & $go.Source env GOROOT
            if ($LASTEXITCODE -eq 0) {
                $goRootGofmt = Join-Path $goRoot "bin\gofmt.exe"
                if (Test-Path $goRootGofmt) {
                    $gofmtPath = $goRootGofmt
                }
            }
        }
    }
    if (-not $gofmtPath) {
        throw "gofmt was not found. Install Go 1.25+ and rerun the build."
    }

    Write-Host "Generating TOM Go wrappers..."
    Push-Location $PSScriptRoot
    try {
        dotnet run --project $generatorProject -c Release --no-restore -- $generatedGo
        if ($LASTEXITCODE -ne 0) {
            throw "TOM Go wrapper generation failed with exit code $LASTEXITCODE"
        }

        & $gofmtPath -w $generatedGo
        if ($LASTEXITCODE -ne 0) {
            throw "gofmt failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }

    $cache["generator"] = $generatorFingerprint
    Save-BuildCache
}
else {
    Write-Host "TOM generator inputs unchanged; using cached Go wrappers."
}

Write-Host "Built $outputDll"
