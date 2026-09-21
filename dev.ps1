$ErrorActionPreference = "Stop"

dotnet watch --no-hot-reload --project (Join-Path $PSScriptRoot "Bimcp.csproj") run -- --output-path (Join-Path $PSScriptRoot "test")
if ($LASTEXITCODE -ne 0) {
    throw "dotnet watch failed with exit code $LASTEXITCODE"
}