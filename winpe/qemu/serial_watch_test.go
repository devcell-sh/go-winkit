package qemu

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatchSerialForEFIShell_DetectsShell(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial.log")

	stop := make(chan struct{})
	defer close(stop)
	ch := WatchSerialForEFIShell(path, stop)

	serial := "UEFI firmware (version edk2-stable202408)\n" +
		"BdsDxe: failed to load Boot0001\n" +
		"BdsDxe: starting Boot0002 \"EFI Internal Shell\" from Fv(64074AFE)\n"
	require.NoError(t, os.WriteFile(path, []byte(serial), 0644))

	select {
	case msg := <-ch:
		assert.Contains(t, msg, "BdsDxe: starting")
		assert.Contains(t, msg, "EFI Internal Shell")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for EFI shell detection")
	}
}

func TestWatchSerialForEFIShell_IgnoresNormalBoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial.log")

	stop := make(chan struct{})
	ch := WatchSerialForEFIShell(path, stop)

	serial := "BdsDxe: starting Boot0001 \"UEFI QEMU QEMU USB HARDDRIVE 1\"\n"
	require.NoError(t, os.WriteFile(path, []byte(serial), 0644))

	select {
	case msg := <-ch:
		t.Fatalf("should not have fired, got: %s", msg)
	case <-time.After(2 * time.Second):
	}
	close(stop)
}

func TestWatchSerialForEFIShell_DetectsWithANSIEscapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial.log")

	stop := make(chan struct{})
	defer close(stop)
	ch := WatchSerialForEFIShell(path, stop)

	serial := "\x1b[2J\x1b[01;01HBdsDxe: starting Boot0004 \"EFI Internal Shell\" from Fv(64074AFE)\n"
	require.NoError(t, os.WriteFile(path, []byte(serial), 0644))

	select {
	case msg := <-ch:
		assert.Contains(t, msg, "EFI Internal Shell")
		assert.NotContains(t, msg, "\x1b")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

func TestWatchSerialForStartupNSHFail_DetectsFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial.log")

	stop := make(chan struct{})
	defer close(stop)
	ch := WatchSerialForStartupNSHFail(path, stop)

	serial := "BOOTAA64.EFI not found on FS0-FS4.\n"
	require.NoError(t, os.WriteFile(path, []byte(serial), 0644))

	select {
	case msg := <-ch:
		assert.NotEmpty(t, msg)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

func TestWatchSerialForDesktopBoot_FiresOnNthBoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "serial.log")

	stop := make(chan struct{})
	defer close(stop)
	ch := WatchSerialForDesktopBoot(path, 2, stop)

	serial := "BdsDxe: starting Boot0006 \"Windows Boot Manager\" from HD(1,GPT,...)\n"
	require.NoError(t, os.WriteFile(path, []byte(serial), 0644))

	select {
	case msg := <-ch:
		t.Fatalf("should not fire on first boot, got: %s", msg)
	case <-time.After(2 * time.Second):
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	require.NoError(t, err)
	f.WriteString("BdsDxe: starting Boot0006 \"Windows Boot Manager\" from HD(1,GPT,...)\n")
	f.Close()

	select {
	case msg := <-ch:
		assert.Contains(t, msg, "#2")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for 2nd boot detection")
	}
}

func TestStripANSI(t *testing.T) {
	assert.Equal(t, "hello world", stripANSI("\x1b[2Jhello \x1b[01;01Hworld"))
	assert.Equal(t, "plain", stripANSI("plain"))
	assert.Equal(t, "", stripANSI(""))
}
