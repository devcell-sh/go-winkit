Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$runtimeRoot = Split-Path -Parent $PSCommandPath
$command = @(
    'run-user',
    '--user', '{{USER_NAME}}',
    '--password', '{{USER_PASSWORD}}',
    '--current-dir', 'E:\',
    '--',
    'E:\Program Files\WSL\wsl.exe'
) + $args

& (Join-Path $runtimeRoot 'winkit-service.exe') @command
exit $LASTEXITCODE
