$ErrorActionPreference = "Stop"

$project = Join-Path $PSScriptRoot "tom\TomBridge\TomBridge.csproj"
$publish = Join-Path $PSScriptRoot "tom\TomBridge\bin\Release\net10.0\win-x64\publish"
$output = Join-Path $PSScriptRoot "tom\bin"
$vsInstaller = "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer"

if (Test-Path (Join-Path $vsInstaller "vswhere.exe")) {
    $env:PATH = "$vsInstaller;$env:PATH"
}

dotnet publish $project -c Release
if ($LASTEXITCODE -ne 0) {
    throw "dotnet publish failed with exit code $LASTEXITCODE"
}
New-Item -ItemType Directory -Force $output | Out-Null
Copy-Item (Join-Path $publish "tombridge.dll") $output -Force

Push-Location $PSScriptRoot
try {
    dotnet run --project .\tom\TomGen\TomGen.csproj -c Release -- .\tom\generated.go
    if ($LASTEXITCODE -ne 0) {
        throw "TOM Go wrapper generation failed with exit code $LASTEXITCODE"
    }
    gofmt -w .\tom\generated.go
    if ($LASTEXITCODE -ne 0) {
        throw "gofmt failed with exit code $LASTEXITCODE"
    }
}
finally {
    Pop-Location
}

Write-Host "Built $output\tombridge.dll"
