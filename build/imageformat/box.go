package imageformat

import (
	"archive/tar"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// packageBox writes a libvirt-format Vagrant box (tar.gz of box.img +
// metadata.json + embedded Vagrantfile) that vagrant-qemu consumes:
//
//	vagrant box add winkit-base.box --name winkit/base
//
// The embedded Vagrantfile bakes in the winssh credentials and ports, so
// a consumer's Vagrantfile needs only config.vm.box = "winkit/base".
func packageBox(qcow2Path, dest string, opts *PackageOpts) error {
	if opts == nil {
		opts = &PackageOpts{}
	}
	vopts := VagrantOpts{}
	if opts.Vagrant != nil {
		vopts = *opts.Vagrant
	}
	vopts.Box = true

	vagrantfile, err := RenderVagrantfile(vopts)
	if err != nil {
		return err
	}

	virtualGB, err := qcow2VirtualSizeGB(qcow2Path)
	if err != nil {
		return fmt.Errorf("reading qcow2 virtual size: %w", err)
	}
	metadata := fmt.Sprintf("{\"provider\":\"libvirt\",\"format\":\"qcow2\",\"virtual_size\":%d}\n", virtualGB)

	img, err := os.Open(qcow2Path)
	if err != nil {
		return fmt.Errorf("opening disk: %w", err)
	}
	defer img.Close()
	imgInfo, err := img.Stat()
	if err != nil {
		return err
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("creating box: %w", err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	for _, entry := range []struct {
		name string
		data string
	}{
		{"metadata.json", metadata},
		{"Vagrantfile", vagrantfile},
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: entry.name, Mode: 0o644, Size: int64(len(entry.data)),
		}); err != nil {
			return err
		}
		if _, err := io.WriteString(tw, entry.data); err != nil {
			return err
		}
	}

	if err := tw.WriteHeader(&tar.Header{
		Name: "box.img", Mode: 0o644, Size: imgInfo.Size(),
	}); err != nil {
		return err
	}
	if _, err := io.Copy(tw, img); err != nil {
		return fmt.Errorf("packing disk into box: %w", err)
	}

	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

// qcow2VirtualSizeGB reads the guest-visible disk size from the qcow2
// header (big-endian u64 at offset 24) and rounds up to whole GB, the
// unit metadata.json's virtual_size uses.
func qcow2VirtualSizeGB(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	header := make([]byte, 32)
	if _, err := io.ReadFull(f, header); err != nil {
		return 0, err
	}
	if string(header[:4]) != "QFI\xfb" {
		return 0, fmt.Errorf("%s is not a qcow2 image", path)
	}
	size := binary.BigEndian.Uint64(header[24:32])
	const gb = 1 << 30
	return int((size + gb - 1) / gb), nil
}
