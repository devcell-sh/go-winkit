package winpe

import (
	"encoding/binary"
	"strings"
	"unicode/utf16"

	"github.com/devcell-sh/go-regedit"
)

// WimPatchSet describes modifications to apply to a single WIM image.
type WimPatchSet struct {
	// ImageNum is the 1-based WIM image index to patch.
	ImageNum int

	// Files maps WIM target paths to host filesystem source paths.
	// Each entry adds a single file.
	Files map[string]string

	// Trees maps WIM target directories to host filesystem source
	// directories. Each entry adds the directory recursively.
	Trees map[string]string

	// DWordPatches are registry DWORD patches grouped by hive path inside
	// the WIM (e.g. `\Windows\System32\config\SYSTEM`).
	DWordPatches []RegistryPatch

	// KeyWrites are full registry key writes (any value type) grouped by
	// hive path inside the WIM.
	KeyWrites []RegistryKeyWrite
}

// RegistryKeyWrite describes a key+value write to a registry hive inside
// a WIM image.
type RegistryKeyWrite struct {
	HivePath string
	KeyPath  string
	Spec     *regedit.Key
}

// EncodeMultiSz encodes a list of strings as REG_MULTI_SZ data:
// each string NUL-terminated in UTF-16LE, with an extra trailing NUL.
func EncodeMultiSz(ss []string) []byte {
	joined := strings.Join(ss, "\x00") + "\x00"
	units := utf16.Encode([]rune(joined + "\x00"))
	out := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[i*2:], u)
	}
	return out
}
