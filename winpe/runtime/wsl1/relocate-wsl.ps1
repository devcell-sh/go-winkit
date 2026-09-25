[CmdletBinding()]
param(
    [string] $Source = 'X:\Program Files\WSL',
    [string] $Destination = 'E:\Program Files\WSL'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

New-Item $Destination -ItemType Directory -Force | Out-Null
Copy-Item (Join-Path $Source '*') $Destination -Recurse -Force

$serviceKey = 'Registry::HKEY_LOCAL_MACHINE\SYSTEM\CurrentControlSet\Services\WSLService'
$proxyKey = 'Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Classes\CLSID\{4EA0C6DD-E9FF-48E7-994E-13A31D10DC60}\InProcServer32'
$hostKey = 'Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Classes\CLSID\{2B9C59C3-98F1-45C8-B87B-12AE3C7927E8}\LocalServer32'
$msiKey = 'Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows\CurrentVersion\Lxss\MSI'

Set-ItemProperty $serviceKey -Name ImagePath -Value '"E:\Program Files\WSL\wslservice.exe"'
Set-Item $proxyKey -Value 'E:\Program Files\WSL\wslserviceproxystub.dll'
Set-Item $hostKey -Value '"E:\Program Files\WSL\wslhost.exe"'
Set-ItemProperty $msiKey -Name InstallLocation -Value 'E:\Program Files\WSL\'

$files = Get-ChildItem $Destination -Recurse -File | Measure-Object -Property Length -Sum
"WSL_RELOCATED files=$($files.Count) bytes=$($files.Sum)"
