package imageformat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFormat(t *testing.T) {
	tests := []struct {
		input string
		want  Format
		err   string
	}{
		{"qcow2", Qcow2, ""},
		{"utm", UTM, ""},
		{"QCOW2", Qcow2, ""},
		{"UTM", UTM, ""},
		{"vmdk", 0, "unknown image format"},
		{"", 0, "unknown image format"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseFormat(tt.input)
			if tt.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestFormatExt(t *testing.T) {
	assert.Equal(t, ".qcow2", Qcow2.Ext())
	assert.Equal(t, ".utm", UTM.Ext())
}

func TestPackageQcow2_CopiesFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "disk.qcow2")
	require.NoError(t, os.WriteFile(src, []byte("QCOW2-DATA"), 0o644))

	dest := filepath.Join(t.TempDir(), "out.qcow2")
	require.NoError(t, Package(Qcow2, src, dest, nil))

	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "QCOW2-DATA", string(data))
}

func TestPackageQcow2_SamePath_NoOp(t *testing.T) {
	src := filepath.Join(t.TempDir(), "disk.qcow2")
	require.NoError(t, os.WriteFile(src, []byte("QCOW2-DATA"), 0o644))

	require.NoError(t, Package(Qcow2, src, src, nil))

	data, err := os.ReadFile(src)
	require.NoError(t, err)
	assert.Equal(t, "QCOW2-DATA", string(data))
}

func TestPackageUTM_CreatesBundle(t *testing.T) {
	src := filepath.Join(t.TempDir(), "disk.qcow2")
	require.NoError(t, os.WriteFile(src, []byte("QCOW2-DATA"), 0o644))

	vars := filepath.Join(t.TempDir(), "efi_vars.fd")
	require.NoError(t, os.WriteFile(vars, []byte("VARS-DATA"), 0o644))

	dest := filepath.Join(t.TempDir(), "Windows.utm")
	require.NoError(t, Package(UTM, src, dest, &PackageOpts{
		VarsPath: vars,
		VMName:   "Test VM",
	}))

	// Bundle directory exists.
	info, err := os.Stat(dest)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// config.plist exists and has expected content.
	plist, err := os.ReadFile(filepath.Join(dest, "config.plist"))
	require.NoError(t, err)
	assert.Contains(t, string(plist), "<key>Backend</key>")
	assert.Contains(t, string(plist), "<string>QEMU</string>")
	assert.Contains(t, string(plist), "<string>Test VM</string>")
	assert.Contains(t, string(plist), "<string>aarch64</string>")
	assert.Contains(t, string(plist), ".qcow2</string>")

	// Data directory has the qcow2 and vars.
	dataDir := filepath.Join(dest, "Data")
	entries, err := os.ReadDir(dataDir)
	require.NoError(t, err)

	var hasQcow2, hasVars bool
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".qcow2") {
			hasQcow2 = true
			data, _ := os.ReadFile(filepath.Join(dataDir, e.Name()))
			assert.Equal(t, "QCOW2-DATA", string(data))
		}
		if e.Name() == "efi_vars.fd" {
			hasVars = true
			data, _ := os.ReadFile(filepath.Join(dataDir, e.Name()))
			assert.Equal(t, "VARS-DATA", string(data))
		}
	}
	assert.True(t, hasQcow2, "Data/ must contain a .qcow2 file")
	assert.True(t, hasVars, "Data/ must contain efi_vars.fd")
}

func TestPackageUTM_NoVars(t *testing.T) {
	src := filepath.Join(t.TempDir(), "disk.qcow2")
	require.NoError(t, os.WriteFile(src, []byte("QCOW2-DATA"), 0o644))

	dest := filepath.Join(t.TempDir(), "Windows.utm")
	require.NoError(t, Package(UTM, src, dest, nil))

	entries, err := os.ReadDir(filepath.Join(dest, "Data"))
	require.NoError(t, err)

	var hasVars bool
	for _, e := range entries {
		if e.Name() == "efi_vars.fd" {
			hasVars = true
		}
	}
	assert.False(t, hasVars, "Data/ must not contain efi_vars.fd when none provided")
}

func TestPackageUTM_ConfigPlistDiskUUID(t *testing.T) {
	src := filepath.Join(t.TempDir(), "disk.qcow2")
	require.NoError(t, os.WriteFile(src, []byte("QCOW2"), 0o644))

	dest := filepath.Join(t.TempDir(), "out.utm")
	require.NoError(t, Package(UTM, src, dest, nil))

	plist, _ := os.ReadFile(filepath.Join(dest, "config.plist"))
	content := string(plist)

	// The disk entry must reference the qcow2 by filename and use NVMe.
	assert.Contains(t, content, "<string>NVMe</string>")
	assert.Contains(t, content, "<string>Disk</string>")
	assert.Contains(t, content, ".qcow2</string>")
}

func TestDefaultOutputName(t *testing.T) {
	assert.Equal(t, "winkit-base.qcow2", DefaultOutputName("base", Qcow2))
	assert.Equal(t, "winkit-core.qcow2", DefaultOutputName("core", Qcow2))
	assert.Equal(t, "winkit-wsl.qcow2", DefaultOutputName("wsl", Qcow2))
	assert.Equal(t, "winkit-base.utm", DefaultOutputName("base", UTM))
	assert.Equal(t, "winkit-wsl.utm", DefaultOutputName("wsl", UTM))
}
