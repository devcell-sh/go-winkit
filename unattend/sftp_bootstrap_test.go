package unattend

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-winkit/isokit"
)

// sftpConfig returns a config with the SFTP share and its payloads enabled,
// the way the wsl2 build sets it up.
func sftpConfig() Config {
	cfg := DefaultConfig()
	cfg.SFTPPort = 9844
	cfg.RclonePayload = RclonePayloadName
	cfg.RclonePayloadData = []byte("PK\x03\x04 fake rclone zip")
	cfg.WinFspPayload = WinFspPayloadName
	cfg.WinFspPayloadData = []byte("MSI fake")
	return cfg
}

// The guest mounts the host SFTP share as a fixed disk: WinFsp installed from
// the answer volume, rclone extracted next to it, and the mount registered as
// an on-start SYSTEM scheduled task. The task shape is load-bearing, proven
// interactively (CELL-532): an SSH-session-launched rclone dies with the
// session and its drive letter is per-logon; the SYSTEM task survives and the
// drive is visible globally, including to WSL1 drvfs.
func TestGenerateBootstrapScript_SFTPMountStep(t *testing.T) {
	ps1 := string(GenerateBootstrapScript(sftpConfig()))

	assert.Contains(t, ps1, WinFspPayloadName, "WinFsp must install from the shipped MSI")
	assert.Contains(t, ps1, RclonePayloadName, "rclone must come from the shipped zip")
	assert.Contains(t, ps1, RcloneMountTaskName, "the mount must be a named scheduled task")
	assert.Contains(t, ps1, "New-ScheduledTaskTrigger -AtStartup", "the task must run at every boot (automount)")
	assert.Contains(t, ps1, "-User SYSTEM", "SYSTEM makes the drive letter global (per-logon otherwise)")
	assert.Contains(t, ps1, "obscure", "the connection-string password must be rclone-obscured")
	assert.Contains(t, ps1, `--volname "winkit"`, "the volume label must default to winkit")
	assert.Contains(t, ps1, "--vfs-disk-space-total-size", "Windows aborts GUI saves when the free-space query fails")
	assert.Contains(t, ps1, "FileSecurity=D:P(A;;FA;;;WD)", "SYSTEM-run WinFsp mounts deny GENERIC_WRITE to other users without this")
	assert.Contains(t, ps1, "--links", "repo symlinks error without --links")
	assert.Contains(t, ps1, "--vfs-cache-mode", "SFTP cannot stream without a write cache")
	assert.Contains(t, ps1, "9844", "the host port must be rendered into the mount")
}

// Without a port the step must be absent entirely — a build with no share
// must not reference payloads that are not there.
func TestGenerateBootstrapScript_NoSFTPStepWithoutPort(t *testing.T) {
	ps1 := string(GenerateBootstrapScript(DefaultConfig()))

	assert.NotContains(t, ps1, RcloneMountTaskName)
	assert.NotContains(t, ps1, WinFspPayloadName)
}

// The WebDAV mapping is replaced by the SFTP fixed-disk mount: WSL1 drvfs
// cannot stat/read through MRxDAV (DRIVE_REMOTE), which was the whole reason
// to switch. The old step must not come back.
func TestGenerateBootstrapScript_NoWebDAVMountStep(t *testing.T) {
	assert.NotContains(t, string(GenerateBootstrapScript(sftpConfig())), "WebDAV share")
	assert.NotContains(t, string(GenerateBootstrapScript(DefaultConfig())), "WebDAV share")
}

// Defaults: user/password/drive fill in when only the port is set.
func TestGenerateBootstrapScript_SFTPDefaults(t *testing.T) {
	cfg := sftpConfig()
	ps1 := string(GenerateBootstrapScript(cfg))

	assert.Contains(t, ps1, "winkit", "default share user")
	assert.Contains(t, ps1, "W:", "default drive letter")
}

// The mount must go through Invoke-Step (log-and-continue), never bare throw
// at top level: a missing host server must not abort provisioning.
func TestGenerateBootstrapScript_SFTPFailureIsNonFatal(t *testing.T) {
	ps1 := string(GenerateBootstrapScript(sftpConfig()))
	assert.Contains(t, ps1, "Invoke-Step 'mount the host SFTP share as a fixed disk'",
		"SFTP mount is not wrapped in Invoke-Step")
}

// The WSL1 rootfs ships as distro.wsl (byte-exact gzip) so the bootstrap's
// drive scan finds it on the answer volume and imports it at first logon.
func TestBuildAnswerVolume_ShipsWSLExact(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WSLPayloadData = []byte("\x1f\x8b fake gzip tarball")
	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, BuildAnswerVolume(cfg, imgPath))

	got, err := isokit.ReadFileFromFAT(imgPath, "/distro.wsl")
	require.NoError(t, err, "distro.wsl must ship on the answer volume")
	assert.Equal(t, string(cfg.WSLPayloadData), strings.TrimRight(string(got), "\x00"),
		"distro.wsl must be byte-exact (trailing zeros aside)")
}

// Both payloads must ship byte-exact: an MSI's signature and a zip's
// end-of-central-directory scan both break on trailing padding.
func TestBuildAnswerVolume_ShipsRcloneAndWinFspExact(t *testing.T) {
	cfg := sftpConfig()
	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, BuildAnswerVolume(cfg, imgPath))

	got, err := isokit.ReadFileFromFAT(imgPath, "/"+RclonePayloadName)
	require.NoError(t, err, "rclone payload must ship on the answer volume")
	assert.Equal(t, string(cfg.RclonePayloadData), strings.TrimRight(string(got), "\x00"),
		"rclone zip must be byte-exact (trailing zeros aside)")

	got, err = isokit.ReadFileFromFAT(imgPath, "/"+WinFspPayloadName)
	require.NoError(t, err, "WinFsp payload must ship on the answer volume")
	assert.Equal(t, string(cfg.WinFspPayloadData), strings.TrimRight(string(got), "\x00"),
		"WinFsp MSI must be byte-exact (trailing zeros aside)")
}
