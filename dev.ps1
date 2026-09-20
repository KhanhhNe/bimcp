$repoRoot = $PSScriptRoot
$env:TOM_BRIDGE_DLL = Join-Path $repoRoot "tom\bin\tombridge.dll"
$testRoot = Join-Path $repoRoot "test"

function Start-Service {
    Write-Host "Starting bimcp..."
    Clear-Host
    Start-Process -FilePath "go" `
        -ArgumentList @("run", ".", "--output-path", $testRoot) `
        -WorkingDirectory $repoRoot `
        -NoNewWindow `
        -PassThru
}

function Stop-Service([System.Diagnostics.Process]$process) {
    if (-not $process.HasExited) {
        & taskkill.exe /PID $process.Id /T /F 2>$null | Out-Null
        $process.WaitForExit()
    }
}

$watcher = [System.IO.FileSystemWatcher]::new($repoRoot, "*.go")
$watcher.IncludeSubdirectories = $true
$watcher.NotifyFilter = [System.IO.NotifyFilters]::FileName -bor [System.IO.NotifyFilters]::LastWrite

$eventNames = @("Changed", "Created", "Deleted", "Renamed")
foreach ($eventName in $eventNames) {
    Register-ObjectEvent -InputObject $watcher -EventName $eventName -SourceIdentifier "bimcp.$eventName" | Out-Null
}

$watcher.EnableRaisingEvents = $true
$process = Start-Service

try {
    while ($true) {
        $event = Wait-Event | Where-Object SourceIdentifier -Like "bimcp.*" | Select-Object -First 1
        Remove-Event -EventIdentifier $event.EventIdentifier
        Get-Event | Where-Object SourceIdentifier -Like "bimcp.*" | Remove-Event

        Write-Host "Go source changed: $($event.SourceEventArgs.FullPath)"
        Stop-Service $process
        $process = Start-Service
    }
}
finally {
    Stop-Service $process
    foreach ($eventName in $eventNames) {
        Unregister-Event -SourceIdentifier "bimcp.$eventName" -ErrorAction SilentlyContinue
    }
    $watcher.Dispose()
}