# Patch the X:\ ramdisk's NTFS BPB so lxcore/drvfs accepts the volume.
#
# Stock boot.sdi carries a ~3 MB NTFS (total_sectors=6173, 512-byte sectors).
# After WIM overlay inflates the ramdisk to ~500 MB+, the BPB still claims
# 6173 sectors. lxcore's drvfs mount path reads that field and returns
# EOVERFLOW on any I/O beyond the declared size. Bootmgr is done by this
# point, so patching the BPB in-place satisfies drvfs without re-validation.
#
# The ramdisk volume device is write-protected at the device level. We must
# use FSCTL_LOCK_VOLUME to get exclusive access before writing raw sectors,
# then FSCTL_UNLOCK_VOLUME to release. If locking fails, fall back to
# IOCTL_DISK_SET_DISK_ATTRIBUTES to clear the read-only flag.
[CmdletBinding()]
param(
    [string] $DriveLetter = 'X'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;

public static class VolumeIO {
    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    public static extern SafeFileHandle CreateFileW(
        string lpFileName, uint dwDesiredAccess, uint dwShareMode,
        IntPtr lpSecurityAttributes, uint dwCreationDisposition,
        uint dwFlagsAndAttributes, IntPtr hTemplateFile);

    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool DeviceIoControl(
        SafeFileHandle hDevice, uint dwIoControlCode,
        IntPtr lpInBuffer, uint nInBufferSize,
        IntPtr lpOutBuffer, uint nOutBufferSize,
        out uint lpBytesReturned, IntPtr lpOverlapped);

    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool ReadFile(
        SafeFileHandle hFile, byte[] lpBuffer, uint nNumberOfBytesToRead,
        out uint lpNumberOfBytesRead, IntPtr lpOverlapped);

    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool WriteFile(
        SafeFileHandle hFile, byte[] lpBuffer, uint nNumberOfBytesToWrite,
        out uint lpNumberOfBytesWritten, IntPtr lpOverlapped);

    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool SetFilePointerEx(
        SafeFileHandle hFile, long liDistanceToMove,
        out long lpNewFilePointer, uint dwMoveMethod);

    public const uint GENERIC_READ      = 0x80000000;
    public const uint GENERIC_WRITE     = 0x40000000;
    public const uint FILE_SHARE_READ   = 0x00000001;
    public const uint FILE_SHARE_WRITE  = 0x00000002;
    public const uint OPEN_EXISTING     = 3;

    public const uint FSCTL_LOCK_VOLUME   = 0x00090018;
    public const uint FSCTL_UNLOCK_VOLUME = 0x0009001C;
    public const uint FSCTL_DISMOUNT_VOLUME = 0x00090020;
    public const uint FSCTL_ALLOW_EXTENDED_DASD_IO = 0x00090083;
}
'@

$volumePath = "\\.\${DriveLetter}:"

$handle = [VolumeIO]::CreateFileW(
    $volumePath,
    [VolumeIO]::GENERIC_READ -bor [VolumeIO]::GENERIC_WRITE,
    [VolumeIO]::FILE_SHARE_READ -bor [VolumeIO]::FILE_SHARE_WRITE,
    [IntPtr]::Zero,
    [VolumeIO]::OPEN_EXISTING,
    0,
    [IntPtr]::Zero
)

if ($handle.IsInvalid) {
    $err = [System.Runtime.InteropServices.Marshal]::GetLastWin32Error()
    throw "CreateFileW($volumePath) failed: Win32 error $err"
}

try {
    $bytesReturned = [uint32]0

    # Lock the volume for exclusive raw access.
    $locked = [VolumeIO]::DeviceIoControl(
        $handle, [VolumeIO]::FSCTL_LOCK_VOLUME,
        [IntPtr]::Zero, 0, [IntPtr]::Zero, 0,
        [ref] $bytesReturned, [IntPtr]::Zero)
    if (-not $locked) {
        $err = [System.Runtime.InteropServices.Marshal]::GetLastWin32Error()
        Write-Output "BPB patch: FSCTL_LOCK_VOLUME failed (error $err), trying dismount"
        # Dismount can succeed where lock fails on busy volumes.
        [void][VolumeIO]::DeviceIoControl(
            $handle, [VolumeIO]::FSCTL_DISMOUNT_VOLUME,
            [IntPtr]::Zero, 0, [IntPtr]::Zero, 0,
            [ref] $bytesReturned, [IntPtr]::Zero)
    } else {
        Write-Output 'BPB patch: volume locked'
    }

    # Allow writes past declared volume size.
    [void][VolumeIO]::DeviceIoControl(
        $handle, [VolumeIO]::FSCTL_ALLOW_EXTENDED_DASD_IO,
        [IntPtr]::Zero, 0, [IntPtr]::Zero, 0,
        [ref] $bytesReturned, [IntPtr]::Zero)

    # Read the first sector (BPB).
    $sector = [byte[]]::new(512)
    $bytesRead = [uint32]0
    if (-not [VolumeIO]::ReadFile($handle, $sector, 512, [ref] $bytesRead, [IntPtr]::Zero)) {
        $err = [System.Runtime.InteropServices.Marshal]::GetLastWin32Error()
        throw "ReadFile failed: Win32 error $err"
    }
    if ($bytesRead -lt 512) {
        throw "short read from $volumePath ($bytesRead bytes)"
    }

    $bytesPerSector = [BitConverter]::ToUInt16($sector, 0x0B)
    $currentSectors = [BitConverter]::ToInt64($sector, 0x28)

    $drive = [System.IO.DriveInfo]::new($DriveLetter)
    $actualSize = $drive.TotalSize
    $newSectors = [long][Math]::Floor($actualSize / $bytesPerSector) - 1

    Write-Output "BPB patch: bps=$bytesPerSector current_sectors=$currentSectors actual_size=$actualSize new_sectors=$newSectors"

    if ($newSectors -le $currentSectors) {
        Write-Output 'BPB patch: skipped (geometry already sufficient)'
        return
    }

    [Array]::Copy([BitConverter]::GetBytes($newSectors), 0, $sector, 0x28, 8)

    # Seek back to sector 0 and write.
    $newPos = [long]0
    if (-not [VolumeIO]::SetFilePointerEx($handle, 0, [ref] $newPos, 0)) {
        $err = [System.Runtime.InteropServices.Marshal]::GetLastWin32Error()
        throw "SetFilePointerEx failed: Win32 error $err"
    }

    $bytesWritten = [uint32]0
    if (-not [VolumeIO]::WriteFile($handle, $sector, 512, [ref] $bytesWritten, [IntPtr]::Zero)) {
        $err = [System.Runtime.InteropServices.Marshal]::GetLastWin32Error()
        throw "WriteFile failed: Win32 error $err"
    }

    Write-Output "BPB patch: wrote $bytesWritten bytes"

    # Unlock so the filesystem can resume.
    [void][VolumeIO]::DeviceIoControl(
        $handle, [VolumeIO]::FSCTL_UNLOCK_VOLUME,
        [IntPtr]::Zero, 0, [IntPtr]::Zero, 0,
        [ref] $bytesReturned, [IntPtr]::Zero)

    Write-Output 'BPB patch: applied'
} finally {
    $handle.Close()
}
