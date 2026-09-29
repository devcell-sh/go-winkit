package winpe

import (
	"strings"

	"github.com/devcell-sh/go-regedit"
)

// NetBIOSNameMax is the hard limit on a Windows computer name.
const NetBIOSNameMax = 15

const defaultGuestHostname = "winkit"

// GuestHostname derives the guest's ComputerName from a cell ID.
//
// It sanitizes the input for NetBIOS: forbidden characters become dashes,
// runs of dashes collapse, and the result is truncated to 15 characters.
// An empty or fully-sanitized-away input returns "winkit".
func GuestHostname(cellID string) string {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|', ' ', '\t':
			return '-'
		}
		return r
	}, cellID)

	for strings.Contains(clean, "--") {
		clean = strings.ReplaceAll(clean, "--", "-")
	}
	clean = strings.Trim(clean, "-")

	if len(clean) > NetBIOSNameMax {
		clean = strings.TrimRight(clean[:NetBIOSNameMax], "-")
	}
	if clean == "" {
		return defaultGuestHostname
	}
	return clean
}

// HostnamePatchSet returns a WimPatchSet that pre-seeds the ComputerName
// registry value in the SYSTEM hive. wpeinit reads this at boot and uses
// it instead of generating a random MININT-xxxxxxx name.
func HostnamePatchSet(imageNum int, hostname string) WimPatchSet {
	return WimPatchSet{
		ImageNum: imageNum,
		KeyWrites: []RegistryKeyWrite{{
			HivePath: systemHivePath,
			KeyPath:  `ControlSet001\Control\ComputerName\ComputerName`,
			Spec: &regedit.Key{
				Values: map[string]regedit.Value{
					"ComputerName": szValue(strings.ToUpper(hostname)),
				},
			},
		}},
	}
}
