package winpe

import "github.com/devcell-sh/go-regedit"

const (
	// setupExecuteCmd is the command smss.exe runs during the Windows
	// second-pass install phase. The --detach flag makes it non-blocking
	// so Setup continues while the pe-agent tails logs. The log file
	// persists on disk so the host can verify the agent ran.
	setupExecuteCmd = `C:\winkit-service.exe run --name pe-agent --detach --log-file C:\winkit-pe-agent.log`

	// PeAgentLogFile is the guest path where the second-pass pe-agent
	// writes its log on the OS drive.
	PeAgentLogFile = `C:\winkit-pe-agent.log`

	// BootVolumeMarker is the filename placed on the FAT boot volume so
	// the pe-agent can discover it by scanning drive letters. The host
	// reads the log back from the same volume after the VM exits.
	BootVolumeMarker = "winkit-boot.marker"

	// BootVolumeLogFile is the filename the pe-agent writes on the boot
	// FAT volume. The host reads it with ReadFileFromFATQcow2.
	BootVolumeLogFile = "winkit-pe-agent.log"

	systemHivePath = `\Windows\System32\config\SYSTEM`
)

// PeAgentPatchSet returns a WimPatchSet that installs the pe-agent into a
// WIM image. serviceExePath is the host path to winkit-service.exe.
//
// For install.wim, this places the binary at \winkit-service.exe and adds a
// SetupExecute registry entry so smss.exe launches it during the second-pass
// install phase.
//
// For boot.wim, pass registryWrite=false: the pe-agent is started by
// gosshd.cmd, so no registry modification is needed. The binary is still
// placed at \winkit\winkit-service.exe (matching the inject tree layout).
func PeAgentPatchSet(imageNum int, serviceExePath string, registryWrite bool) WimPatchSet {
	ps := WimPatchSet{ImageNum: imageNum}

	if registryWrite {
		ps.Files = map[string]string{
			`\winkit-service.exe`: serviceExePath,
		}
		ps.KeyWrites = []RegistryKeyWrite{{
			HivePath: systemHivePath,
			KeyPath:  `Setup`,
			Spec: &regedit.Key{
				Values: map[string]regedit.Value{
					"SetupExecute": {
						Type: regedit.TypeMultiString,
						Data: EncodeMultiSz([]string{setupExecuteCmd}),
					},
				},
			},
		}}
	} else {
		ps.Files = map[string]string{
			`\winkit\` + ServiceVolumeName: serviceExePath,
		}
	}

	return ps
}
