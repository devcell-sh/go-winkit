[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string] $UserName,
    [ValidateSet(0, 1)]
    [int] $NewDistributionLxFs = 0,
    [string] $ProfileRoot = 'E:\Users',
    [string] $RuntimePath = 'E:\Program Files\WSL',
    [string] $DistroPath = 'E:\wsl-winkit'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$sidLookupSource = @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Security.Principal;
using System.Text;

public static class SidLookup
{
    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool LookupAccountName(
        string systemName,
        string accountName,
        byte[] sid,
        ref uint sidSize,
        StringBuilder referencedDomainName,
        ref uint domainSize,
        out int sidUse);

    public static string ForAccount(string accountName)
    {
        uint sidSize = 0;
        uint domainSize = 0;
        int sidUse;
        LookupAccountName(null, accountName, null, ref sidSize, null, ref domainSize, out sidUse);
        var sid = new byte[sidSize];
        var domain = new StringBuilder((int)domainSize);
        if (!LookupAccountName(null, accountName, sid, ref sidSize, domain, ref domainSize, out sidUse))
            throw new Win32Exception(Marshal.GetLastWin32Error(), "LookupAccountName failed");
        return new SecurityIdentifier(sid, 0).Value;
    }
}
'@

Add-Type -TypeDefinition $sidLookupSource
$sid = [SidLookup]::ForAccount($UserName)
$profilePath = Join-Path $ProfileRoot $UserName
$homeDrive = Split-Path -Qualifier $profilePath
$homePath = $profilePath.Substring($homeDrive.Length)
$profileKey = "Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\$sid"
$userHive = "Registry::HKEY_USERS\$sid"

if (-not (Test-Path $profilePath)) {
    Copy-Item 'X:\Users\Default' $profilePath -Recurse -Force
}
New-Item $DistroPath -ItemType Directory -Force | Out-Null

& icacls.exe $profilePath /grant "*${sid}:(OI)(CI)F" /T /C | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw "granting profile access failed with exit code $LASTEXITCODE"
}
& icacls.exe $RuntimePath /grant "*${sid}:(OI)(CI)RX" /T /C | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw "granting WSL runtime access failed with exit code $LASTEXITCODE"
}
& icacls.exe $DistroPath /grant "*${sid}:(OI)(CI)F" /T /C | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw "granting WSL distro access failed with exit code $LASTEXITCODE"
}

New-Item $profileKey -Force | Out-Null
New-ItemProperty $profileKey -Name ProfileImagePath -PropertyType ExpandString -Value $profilePath -Force | Out-Null
New-ItemProperty $profileKey -Name Flags -PropertyType DWord -Value 0 -Force | Out-Null
New-ItemProperty $profileKey -Name State -PropertyType DWord -Value 0 -Force | Out-Null
New-ItemProperty $profileKey -Name RefCount -PropertyType DWord -Value 0 -Force | Out-Null

if (-not (Test-Path $userHive)) {
    & reg.exe load "HKU\$sid" "$profilePath\NTUSER.DAT" | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "reg load failed with exit code $LASTEXITCODE"
    }
}

$environmentKey = "$userHive\Environment"
New-Item $environmentKey -Force | Out-Null
New-ItemProperty $environmentKey -Name USERPROFILE -PropertyType ExpandString -Value $profilePath -Force | Out-Null
New-ItemProperty $environmentKey -Name HOMEDRIVE -PropertyType String -Value $homeDrive -Force | Out-Null
New-ItemProperty $environmentKey -Name HOMEPATH -PropertyType String -Value $homePath -Force | Out-Null
New-ItemProperty $environmentKey -Name TEMP -PropertyType ExpandString -Value "$profilePath\AppData\Local\Temp" -Force | Out-Null
New-ItemProperty $environmentKey -Name TMP -PropertyType ExpandString -Value "$profilePath\AppData\Local\Temp" -Force | Out-Null
New-ItemProperty $environmentKey -Name WSL_UTF8 -PropertyType String -Value '1' -Force | Out-Null
New-Item "$profilePath\AppData\Local\Temp" -ItemType Directory -Force | Out-Null

$lxssKey = "$userHive\Software\Microsoft\Windows\CurrentVersion\Lxss"
New-Item $lxssKey -Force | Out-Null
New-ItemProperty $lxssKey -Name DefaultVersion -PropertyType DWord -Value 1 -Force | Out-Null
New-ItemProperty $lxssKey -Name NewDistributionLxFs -PropertyType DWord -Value $NewDistributionLxFs -Force | Out-Null

"PROFILE_READY SID=$sid PATH=$profilePath RUNTIME=$RuntimePath DISTRO=$DistroPath"
