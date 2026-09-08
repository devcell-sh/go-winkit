package imageformat

import (
	"archive/tar"
	"compress/gzip"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeQcow2 writes a minimal qcow2 header claiming a 64 GB virtual
// size, followed by some payload bytes.
func writeFakeQcow2(t *testing.T, path string) {
	t.Helper()
	header := make([]byte, 72)
	copy(header, "QFI\xfb")
	binary.BigEndian.PutUint32(header[4:8], 3) // version
	binary.BigEndian.PutUint64(header[24:32], 64<<30)
	if err := os.WriteFile(path, append(header, []byte("diskdata")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPackageBox(t *testing.T) {
	dir := t.TempDir()
	qcow2 := filepath.Join(dir, "disk.qcow2")
	writeFakeQcow2(t, qcow2)
	dest := filepath.Join(dir, "winkit-base.box")

	err := Package(Box, qcow2, dest, &PackageOpts{
		Vagrant: &VagrantOpts{Username: "dmitry", Password: "rdp", SSHPort: 10122},
	})
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)

	entries := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		entries[hdr.Name] = string(data)
	}

	meta, ok := entries["metadata.json"]
	if !ok {
		t.Fatal("metadata.json missing from box")
	}
	for _, want := range []string{`"provider":"libvirt"`, `"format":"qcow2"`, `"virtual_size":64`} {
		if !strings.Contains(meta, want) {
			t.Errorf("metadata.json missing %q: %s", want, meta)
		}
	}

	vf, ok := entries["Vagrantfile"]
	if !ok {
		t.Fatal("Vagrantfile missing from box")
	}
	for _, want := range []string{
		`config.winssh.username = ENV.fetch("WINKIT_USERNAME", "dmitry")`,
		`qe.ssh_port = Integer(ENV.fetch("WINKIT_SSH_PORT", 10122))`,
	} {
		if !strings.Contains(vf, want) {
			t.Errorf("embedded Vagrantfile missing %q", want)
		}
	}
	if strings.Contains(vf, "image_path") {
		t.Error("embedded Vagrantfile must not set image_path (the box supplies the disk)")
	}

	img, ok := entries["box.img"]
	if !ok {
		t.Fatal("box.img missing from box")
	}
	if !strings.HasSuffix(img, "diskdata") || !strings.HasPrefix(img, "QFI\xfb") {
		t.Error("box.img content does not match the source qcow2")
	}
}

func TestQcow2VirtualSizeRejectsNonQcow2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not.qcow2")
	if err := os.WriteFile(path, make([]byte, 64), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := qcow2VirtualSizeGB(path); err == nil {
		t.Fatal("expected error for non-qcow2 file")
	}
}
