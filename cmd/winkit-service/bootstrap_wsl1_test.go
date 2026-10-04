package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// windowsAbsPath matches drive-letter paths like E:\foo or X:\bar.
var windowsAbsPath = regexp.MustCompile(`^[A-Z]:\\`)

func TestDefaultWSL1BootstrapConfig(t *testing.T) {
	cfg := defaultWSL1BootstrapConfig(`X:\winkit`)

	assert.Equal(t, byte('E'), cfg.DriveLetter)
	assert.Equal(t, "winkit", cfg.UserName)
	assert.Equal(t, "winkit", cfg.DistroName)

	for name, path := range map[string]string{
		"RuntimeRoot":  cfg.RuntimeRoot,
		"ServicePath":  cfg.ServicePath,
		"WSLSource":    cfg.WSLSource,
		"WSLDest":      cfg.WSLDest,
		"DistroPath":   cfg.DistroPath,
		"ProfileRoot":  cfg.ProfileRoot,
		"CatalogDir":   cfg.CatalogDir,
		"DiskpartFile": cfg.DiskpartFile,
	} {
		assert.True(t, windowsAbsPath.MatchString(path),
			"%s = %q must be an absolute Windows path", name, path)
	}
}

func TestBootstrapPathFormats(t *testing.T) {
	cfg := defaultWSL1BootstrapConfig(`X:\winkit`)

	paths := map[string]string{
		"drive":        fmt.Sprintf("%c:\\", cfg.DriveLetter),
		"pagefile":     fmt.Sprintf("/path=%c:\\pagefile.sys", cfg.DriveLetter),
		"probeOK":      filepath.Join(fmt.Sprintf("%c:\\winkit", cfg.DriveLetter), "probe.ok"),
		"winkitDir":    fmt.Sprintf("%c:\\winkit", cfg.DriveLetter),
		"distroSearch": fmt.Sprintf("%c:\\distro.wsl", cfg.DriveLetter),
		"volumeDevice": fmt.Sprintf(`\\.\%c:`, cfg.DriveLetter),
		"driveRoot":    fmt.Sprintf("%c:\\", cfg.DriveLetter),
	}

	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			switch {
			case name == "pagefile":
				require.Contains(t, path, `E:\pagefile.sys`,
					"pagefile must include drive letter + backslash separator")
				require.False(t, strings.Contains(path, "Epagefile"),
					"pagefile path must not merge drive letter with filename")
			case name == "volumeDevice":
				assert.Equal(t, `\\.\E:`, path)
			default:
				assert.True(t, strings.HasPrefix(path, "E:\\"),
					"%s = %q must start with E:\\", name, path)
			}
		})
	}
}

func TestBootstrapPathsNoDriveMerge(t *testing.T) {
	for _, letter := range []byte{'C', 'D', 'E', 'F'} {
		t.Run(string(letter), func(t *testing.T) {
			pagefile := fmt.Sprintf("/path=%c:\\pagefile.sys", letter)
			assert.Contains(t, pagefile, fmt.Sprintf("%c:\\", letter),
				"pagefile path must contain drive letter + colon + backslash")

			winkitDir := fmt.Sprintf("%c:\\winkit", letter)
			assert.True(t, strings.HasPrefix(winkitDir, fmt.Sprintf("%c:\\", letter)))

			distro := fmt.Sprintf("%c:\\distro.wsl", letter)
			assert.True(t, strings.HasPrefix(distro, fmt.Sprintf("%c:\\", letter)))
		})
	}
}

func TestDefaultConfigPathsReferenceCorrectDrive(t *testing.T) {
	cfg := defaultWSL1BootstrapConfig(`X:\winkit`)

	writableDrive := fmt.Sprintf("%c:\\", cfg.DriveLetter)
	const ramdisk = `X:\`

	for name, tt := range map[string]struct {
		path   string
		prefix string
	}{
		"WSLSource":   {cfg.WSLSource, ramdisk},
		"CatalogDir":  {cfg.CatalogDir, ramdisk},
		"WSLDest":     {cfg.WSLDest, writableDrive},
		"DistroPath":  {cfg.DistroPath, writableDrive},
		"ProfileRoot": {cfg.ProfileRoot, writableDrive},
	} {
		t.Run(name, func(t *testing.T) {
			assert.True(t, strings.HasPrefix(tt.path, tt.prefix),
				"%s = %q should start with %s", name, tt.path, tt.prefix)
		})
	}
}
