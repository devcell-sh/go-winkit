Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$runtimeRoot = Split-Path -Parent $PSCommandPath
$service = Join-Path $runtimeRoot 'winkit-service.exe'
$log = Join-Path $runtimeRoot 'wsl1-bootstrap.log'
$okMarker = Join-Path $runtimeRoot 'wsl1-bootstrap.ok'
$failedMarker = Join-Path $runtimeRoot 'wsl1-bootstrap.failed'
$userName = '{{USER_NAME}}'
$password = '{{USER_PASSWORD}}'

Remove-Item $log, $okMarker, $failedMarker -Force -ErrorAction SilentlyContinue

function Write-BootstrapLog {
    param([Parameter(Mandatory)][AllowEmptyString()][string] $Message)

    $line = '{0:o} {1}' -f [DateTime]::UtcNow, $Message
    Add-Content -Path $log -Value $line -Encoding utf8
    Write-Output $line
}

function Invoke-NativeChecked {
    param(
        [Parameter(Mandatory)][string] $FilePath,
        [string[]] $ArgumentList = @()
    )

    Write-BootstrapLog "> $FilePath $($ArgumentList -join ' ')"
    $output = @(& $FilePath @ArgumentList 2>&1)
    $exitCode = $LASTEXITCODE
    foreach ($line in $output) {
        Write-BootstrapLog ([string] $line)
    }
    if ($exitCode -ne 0) {
        throw "$FilePath failed with exit code $exitCode"
    }
}

try {
    Write-BootstrapLog 'Registering WSL1 component catalogs'
    Invoke-NativeChecked $service @('add-catalogs', '--dir', 'X:\Windows\winkit\WSL1Catalogs')

    foreach ($name in @('bfs', 'bindflt', 'afunix', 'wcifs', 'P9Rdr')) {
        Write-BootstrapLog "Ensuring service $name is running"
        Invoke-NativeChecked $service @('ensure-service', '--name', $name)
    }

    if (-not (Test-Path 'E:\')) {
        Write-BootstrapLog 'Preparing the writable WSL1 disk'
        Invoke-NativeChecked 'diskpart.exe' @('/s', (Join-Path $runtimeRoot 'prepare-wsl1-disk.txt'))
    }
    if (-not (Test-Path 'E:\')) {
        throw 'writable WSL1 volume E: is unavailable after disk preparation'
    }

    Write-BootstrapLog 'Creating the WSL1 pagefile'
    Invoke-NativeChecked 'wpeutil.exe' @('CreatePageFile', '/path=E:\pagefile.sys', '/size=4096')

    Write-BootstrapLog 'Relocating the packaged WSL runtime to NTFS'
    & (Join-Path $runtimeRoot 'relocate-wsl.ps1') 2>&1 |
        ForEach-Object { Write-BootstrapLog ([string] $_) }

    Write-BootstrapLog 'Ensuring WSLService is running'
    Invoke-NativeChecked $service @('ensure-service', '--name', 'WSLService')

    Write-BootstrapLog "Ensuring local WSL user $userName exists"
    Invoke-NativeChecked $service @('ensure-user', '--user', $userName, '--password', $password, '--admin')

    Write-BootstrapLog 'Preparing the WSL user profile and registry'
    & (Join-Path $runtimeRoot 'setup-wsl-user.ps1') -UserName $userName -ProfileRoot 'E:\Users' `
        -RuntimePath 'E:\Program Files\WSL' -DistroPath 'E:\wsl-winkit' -NewDistributionLxFs 0 2>&1 |
        ForEach-Object { Write-BootstrapLog ([string] $_) }

    Write-BootstrapLog 'Importing and probing the Alpine WSL1 distro'
    & (Join-Path $runtimeRoot 'probe-wsl1.ps1') -UserName $userName -Password $password -ServicePath $service 2>&1 |
        ForEach-Object { Write-BootstrapLog ([string] $_) }
    if (-not (Test-Path 'E:\winkit\probe.ok')) {
        throw 'WSL1 probe completed without its success marker'
    }

    Set-Content $okMarker 'WSL1_BOOTSTRAP_OK' -Encoding ascii
    Write-BootstrapLog 'WSL1_BOOTSTRAP_OK'
} catch {
    $failure = "WSL1_BOOTSTRAP_FAILED: $($_.Exception.Message)"
    Write-BootstrapLog $failure
    Write-BootstrapLog ($_ | Out-String)
    Set-Content $failedMarker $failure -Encoding utf8
}
