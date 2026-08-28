package winpe

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"strings"

	"github.com/devcell-sh/go-winkit/isokit"
)

// ISOInfo holds the result of an ISO preflight check.
type ISOInfo struct {
	Size       int64
	Format     string // "udf", "iso9660", or "unknown"
	HasBootEFI bool
}

// ISOPreflight validates a Windows ISO without parsing its filesystem.
func ISOPreflight(isoPath string) (*ISOInfo, error) {
	return isoPreflight(isoPath, 1<<30)
}

func isoPreflight(isoPath string, minSize int64) (*ISOInfo, error) {
	f, err := os.Open(isoPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open ISO: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot stat ISO: %w", err)
	}

	info := &ISOInfo{Size: stat.Size()}

	if minSize > 0 && stat.Size() < minSize {
		return info, fmt.Errorf("ISO is too small (%d bytes) — expected a Windows installer (>%d bytes)", stat.Size(), minSize)
	}

	info.Format = detectISOFormat(f)

	info.HasBootEFI = scanForBootEFI(f, stat.Size())
	if !info.HasBootEFI {
		return info, fmt.Errorf("BOOTAA64.EFI not found in ISO metadata — this may not be an ARM64 Windows installer")
	}

	return info, nil
}

func detectISOFormat(f *os.File) string {
	buf := make([]byte, 6)
	hasISO, hasUDF := false, false
scan:
	for sector := int64(16); sector < 48; sector++ {
		if _, err := f.ReadAt(buf, sector*2048); err != nil {
			break
		}
		switch string(buf[1:6]) {
		case "CD001":
			hasISO = true
		case "NSR02", "NSR03":
			hasUDF = true
		case "BEA01", "TEA01", "BOOT2":
		default:
			break scan
		}
	}

	switch {
	case hasISO:
		return "iso9660"
	case hasUDF:
		return "udf"
	default:
		return "unknown"
	}
}

func scanForBootEFI(f *os.File, size int64) bool {
	needle := []byte("BOOTAA64.EFI")

	scan := func(offset int64, length int64) bool {
		if offset < 0 {
			offset = 0
		}
		if offset+length > size {
			length = size - offset
		}
		buf := make([]byte, length)
		n, _ := f.ReadAt(buf, offset)
		return bytes.Contains(buf[:n], needle)
	}

	if scan(0, 4*1024*1024) {
		return true
	}

	return false
}

// WindowsISOBootable reports whether firmware can boot an installer ISO.
func WindowsISOBootable(isoPath string) error {
	return isokit.RequireEFIBootable(isoPath)
}

// InstallerBootloader returns the installer's BOOTAA64.EFI.
func InstallerBootloader(isoPath string) ([]byte, error) {
	data, isoErr := isokit.ReadFileFromISO(isoPath, "/EFI/BOOT/BOOTAA64.EFI")
	if isoErr == nil {
		return data, nil
	}
	data, sidecarErr := os.ReadFile(isokit.BootloaderSidecarPath(isoPath))
	if sidecarErr == nil {
		return data, nil
	}
	return nil, fmt.Errorf("no bootloader source: ISO readers: %v; sidecar: %v", isoErr, sidecarErr)
}

var vioscsiISODirs = []string{
	"vioscsi/w11/ARM64",
	"vioscsi/2k25/ARM64",
	"vioscsi/2k22/ARM64",
}

const winPEDriverDir = "/drivers/vioscsi/"

// LoadWinPEStorageDrivers extracts the ARM64 vioscsi driver from the
// virtio-win ISO, keyed by answer-volume path.
func LoadWinPEStorageDrivers(virtioISO string) (map[string][]byte, error) {
	var probeErrs []string
	for _, dir := range vioscsiISODirs {
		inf, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/vioscsi.inf")
		if err != nil {
			probeErrs = append(probeErrs, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		drivers := map[string][]byte{winPEDriverDir + "vioscsi.inf": inf}
		for _, name := range []string{"vioscsi.sys", "vioscsi.cat"} {
			data, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/"+name)
			if err != nil {
				return nil, fmt.Errorf("reading %s/%s: %w", dir, name, err)
			}
			drivers[winPEDriverDir+name] = data
		}
		return drivers, nil
	}
	return nil, fmt.Errorf("no ARM64 vioscsi driver in %s:\n  %s", virtioISO, strings.Join(probeErrs, "\n  "))
}

var vioserialISODirs = []string{
	"vioserial/w11/ARM64",
	"vioserial/2k25/ARM64",
	"vioserial/2k22/ARM64",
}

const winPEVioserialDir = "/drivers/vioserial/"

// LoadWinPEVioserialDrivers extracts the ARM64 vioserial driver from the
// virtio-win ISO.
func LoadWinPEVioserialDrivers(virtioISO string) (map[string][]byte, error) {
	var probeErrs []string
	for _, dir := range vioserialISODirs {
		inf, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/vioser.inf")
		if err != nil {
			probeErrs = append(probeErrs, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		drivers := map[string][]byte{winPEVioserialDir + "vioser.inf": inf}
		for _, name := range []string{"vioser.sys", "vioser.cat"} {
			data, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/"+name)
			if err != nil {
				return nil, fmt.Errorf("reading %s/%s: %w", dir, name, err)
			}
			drivers[winPEVioserialDir+name] = data
		}
		return drivers, nil
	}
	return nil, fmt.Errorf("no ARM64 vioserial driver in %s:\n  %s", virtioISO, strings.Join(probeErrs, "\n  "))
}

var viofsISODirs = []string{
	"viofs/w11/ARM64",
	"viofs/2k25/ARM64",
	"viofs/2k22/ARM64",
}

// LoadWinPEViofsDrivers extracts the ARM64 viofs driver and virtiofs.exe
// mount helper from the virtio-win ISO.
func LoadWinPEViofsDrivers(virtioISO string) (map[string][]byte, error) {
	var probeErrs []string
	for _, dir := range viofsISODirs {
		inf, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/viofs.inf")
		if err != nil {
			probeErrs = append(probeErrs, fmt.Sprintf("%s: %v", dir, err))
			continue
		}
		drivers := map[string][]byte{"/drivers/viofs/viofs.inf": inf}
		for _, name := range []string{"viofs.sys", "viofs.cat"} {
			data, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/"+name)
			if err != nil {
				return nil, fmt.Errorf("reading %s/%s: %w", dir, name, err)
			}
			drivers["/drivers/viofs/"+name] = data
		}
		exe, err := isokit.ReadFileFromISO(virtioISO, "/"+dir+"/virtiofs.exe")
		if err != nil {
			return nil, fmt.Errorf("reading %s/virtiofs.exe: %w", dir, err)
		}
		drivers["/drivers/viofs/virtiofs.exe"] = exe
		return drivers, nil
	}
	return nil, fmt.Errorf("no ARM64 viofs driver in %s:\n  %s", virtioISO, strings.Join(probeErrs, "\n  "))
}

// BootloaderInfo describes the EFI bootloader extracted from a Windows ISO.
type BootloaderInfo struct {
	Arch string // "aarch64", "x86_64", or "unknown"
	Size int
}

// ValidateBootloaderPE checks that raw EFI bootloader bytes are a valid
// aarch64 PE binary.
func ValidateBootloaderPE(data []byte) (*BootloaderInfo, error) {
	if len(data) < 0x86 {
		return nil, fmt.Errorf("BOOTAA64.EFI is too small to be a valid PE binary (%d bytes)", len(data))
	}

	if data[0] != 'M' || data[1] != 'Z' {
		return nil, fmt.Errorf("BOOTAA64.EFI is not a PE binary (missing MZ magic, got %02x%02x)", data[0], data[1])
	}

	peOffset := binary.LittleEndian.Uint32(data[0x3C:])
	if int(peOffset)+6 > len(data) {
		return nil, fmt.Errorf("BOOTAA64.EFI has invalid PE header offset (%d, file is %d bytes)", peOffset, len(data))
	}

	if data[peOffset] != 'P' || data[peOffset+1] != 'E' || data[peOffset+2] != 0 || data[peOffset+3] != 0 {
		return nil, fmt.Errorf("BOOTAA64.EFI has invalid PE signature at offset %d", peOffset)
	}

	machine := binary.LittleEndian.Uint16(data[peOffset+4:])
	arch := peMachineArch(machine)

	if arch != "aarch64" {
		return nil, fmt.Errorf("BOOTAA64.EFI has wrong architecture: expected aarch64, got %s (PE machine 0x%04X)", arch, machine)
	}

	return &BootloaderInfo{Arch: arch, Size: len(data)}, nil
}

func peMachineArch(machine uint16) string {
	switch machine {
	case 0xAA64:
		return "aarch64"
	case 0x8664:
		return "x86_64"
	case 0x014C:
		return "i386"
	default:
		return fmt.Sprintf("unknown(0x%04X)", machine)
	}
}
