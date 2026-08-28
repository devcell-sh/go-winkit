package winpe

import "strings"

// NetBIOSNameMax is the hard limit on a Windows computer name.
const NetBIOSNameMax = 15

const defaultGuestHostname = "devcell"

// GuestHostname derives the guest's ComputerName from a cell ID.
//
// It sanitizes the input for NetBIOS: forbidden characters become dashes,
// runs of dashes collapse, and the result is truncated to 15 characters.
// An empty or fully-sanitized-away input returns "devcell".
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
