[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $UserName,
    [Parameter(Mandatory)][string] $Password,
    [string] $ServicePath = 'X:\winkit\winkit-service.exe'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$distroName = '{{DISTRO_NAME}}'
$work = 'E:\winkit'
$target = "E:\wsl-$distroName"
$wsl = 'E:\Program Files\WSL\wsl.exe'
New-Item $work -ItemType Directory -Force | Out-Null

$distroFile = Get-PSDrive -PSProvider FileSystem |
    ForEach-Object { Join-Path "$($_.Name):\" 'distro.wsl' } |
    Where-Object { Test-Path $_ } |
    Select-Object -First 1
if (-not $distroFile) {
    throw 'distro.wsl was not found on any filesystem drive'
}

function Invoke-WSL {
    param([Parameter(Mandatory)][string[]] $ArgumentList)

    $runArguments = @(
        'run-user', '--user', $UserName, '--password', $Password,
        '--current-dir', 'E:\', '--', $wsl
    ) + $ArgumentList
    $output = @(& $ServicePath @runArguments 2>&1)
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        throw "user-context wsl.exe $($ArgumentList -join ' ') failed with exit code ${exitCode}: $($output -join [Environment]::NewLine)"
    }
    return $output
}

$distros = @(Invoke-WSL -ArgumentList @('-l', '-q')) |
    ForEach-Object { ([string] $_).Replace("`0", '').Trim() } |
    Where-Object { $_ }
$distros | Set-Content (Join-Path $work 'distros.out') -Encoding utf8

if ($distros -notcontains $distroName) {
    Invoke-WSL -ArgumentList @('--import', $distroName, $target, $distroFile, '--version', '1') |
        Set-Content (Join-Path $work 'import.out') -Encoding utf8
}
if (-not (Test-Path "$target\rootfs")) {
    throw "WSL1 rootfs was not created at $target"
}

Invoke-WSL -ArgumentList @('-l', '-v') |
    Set-Content (Join-Path $work 'list.out') -Encoding utf8
Invoke-WSL -ArgumentList @('-d', $distroName, '--exec', '/bin/uname', '-m') |
    Set-Content (Join-Path $work 'arch.out') -Encoding utf8

# Distro-specific identity files: not every rootfs ships /etc/os-release or
# /etc/alpine-release (nixos/nix is a stripped image with neither). Write
# whatever is available; the E2E tests check only the files their distro
# is expected to have.
try {
    Invoke-WSL -ArgumentList @('-d', $distroName, '--exec', '/bin/cat', '/etc/os-release') |
        Set-Content (Join-Path $work 'os-release.out') -Encoding utf8
} catch {
    Set-Content (Join-Path $work 'os-release.out') '' -Encoding utf8
}
try {
    Invoke-WSL -ArgumentList @('-d', $distroName, '--exec', '/bin/cat', '/etc/alpine-release') |
        Set-Content (Join-Path $work 'alpine-release.out') -Encoding utf8
} catch {
    Set-Content (Join-Path $work 'alpine-release.out') '' -Encoding utf8
}

# Nix verify: runs nix --version via the default profile's absolute path
# so it works without a login shell. No-op for non-nix distros.
try {
    Invoke-WSL -ArgumentList @('-d', $distroName, '--exec',
        '/nix/var/nix/profiles/default/bin/nix', '--version') |
        Set-Content (Join-Path $work 'nix-version.out') -Encoding utf8
} catch {
    Set-Content (Join-Path $work 'nix-version.out') '' -Encoding utf8
}

Set-Content (Join-Path $work 'probe.ok') 'WSL1_PROBE_OK' -Encoding ascii
'WSL1_PROBE_OK'
