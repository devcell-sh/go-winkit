package winpe

// Boot-media plumbing shared by answer volumes (unattend) and the WIM-builder
// shared volume (prep_wim). Lives in winpe — the lowest package that needs it —
// so unattend can keep importing winpe without a cycle.

// StartupNSH is the UEFI shell startup script that boots the Windows installer.
// UEFI ignores BIOS-style `-boot d`, so we need this to chain-load the Windows
// EFI boot loader. Uses sequential if-exist checks (not a for loop) because
// UEFI Shell %var expansion inside path strings is unreliable across EDK II builds.
const StartupNSH = `echo Searching for Windows EFI boot loader...
if exist FS0:\EFI\BOOT\BOOTAA64.EFI then
  FS0:\EFI\BOOT\BOOTAA64.EFI
endif
if exist FS1:\EFI\BOOT\BOOTAA64.EFI then
  FS1:\EFI\BOOT\BOOTAA64.EFI
endif
if exist FS2:\EFI\BOOT\BOOTAA64.EFI then
  FS2:\EFI\BOOT\BOOTAA64.EFI
endif
if exist FS3:\EFI\BOOT\BOOTAA64.EFI then
  FS3:\EFI\BOOT\BOOTAA64.EFI
endif
if exist FS4:\EFI\BOOT\BOOTAA64.EFI then
  FS4:\EFI\BOOT\BOOTAA64.EFI
endif
echo BOOTAA64.EFI not found on FS0-FS4. Listing all FS devices:
map -r
`

// fatClusterSize is the cluster geometry of the small images CreateFATImage
// builds.
const fatClusterSize = 2048

// PadForFAT appends newlines until the payload is an exact multiple of the
// cluster size. go-diskfs v1.9.4 mis-records the directory-entry size of
// files ending near a cluster boundary — first measured as the last 63 bytes
// (6129-byte file), later disproven by a 14270-byte file corrupting 66 bytes
// short — so no partial-cluster tail is trusted; cluster-aligned files are
// the one class that has never mis-recorded. Trailing whitespace is legal
// after an XML root element, in PowerShell, and in UEFI .nsh scripts, so the
// padding is safe for every file we write. isokit.CreateFATImage still
// verifies the round-trip, so if even this assumption falls it fails loudly
// rather than silently shipping a corrupt image.
func PadForFAT(data []byte) []byte {
	rem := len(data) % fatClusterSize
	if rem == 0 {
		return data
	}
	padding := fatClusterSize - rem
	out := make([]byte, 0, len(data)+padding)
	out = append(out, data...)
	for i := 0; i < padding; i++ {
		out = append(out, '\n')
	}
	return out
}

// StructuredPortName is the virtio-serial port carrying structured JSON
// build events (build.jsonl). Guest scripts open `\\.\Global\<name>` by this
// exact string, so it is part of the guest/host contract, not a QEMU detail.
const StructuredPortName = `devcell.structured.0`
