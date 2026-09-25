Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$runtimeRoot = Split-Path -Parent $PSCommandPath
$okMarker = Join-Path $runtimeRoot 'wsl1-bootstrap.ok'
$failedMarker = Join-Path $runtimeRoot 'wsl1-bootstrap.failed'
$log = Join-Path $runtimeRoot 'wsl1-bootstrap.log'

if (Test-Path $okMarker) {
    Get-Content $okMarker
    exit 0
}
if (Test-Path $failedMarker) {
    Get-Content $failedMarker
    if (Test-Path $log) {
        Get-Content $log
    }
    exit 1
}

'WSL1_BOOTSTRAP_PENDING'
exit 2
