$ErrorActionPreference = "Stop"

dotnet build (Join-Path $PSScriptRoot "Bimcp.csproj") -c Release
if ($LASTEXITCODE -ne 0) {
    throw "dotnet build failed with exit code $LASTEXITCODE"
}
