package imageformat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteVagrantfile(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "Vagrantfile")
	err := WriteVagrantfile(dest, VagrantOpts{
		ImageName: "winkit-base.qcow2",
		Username:  "dmitry",
		Password:  "rdp",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		`File.expand_path("winkit-base.qcow2", __dir__)`,
		`config.winssh.username = ENV.fetch("WINKIT_USERNAME", "dmitry")`,
		`config.winssh.password = ENV.fetch("WINKIT_PASSWORD", "rdp")`,
		`qe.ssh_port = Integer(ENV.fetch("WINKIT_SSH_PORT", 50022))`,
		`qe.ssh_auto_correct = true`,
		`qe.drive_interface = "none"`,
		`qe.net_device = "virtio-net-pci"`,
		`"-device", "nvme,drive=disk0,serial=winkit0,bootindex=0",`,
		`"-device", "ramfb",`,
		`config.vm.communicator = "winssh"`,
		`config.ssh.insert_key = false`,
		`qe.memory = "6G"`,
		`qe.smp = 4`,
		`config.vm.network "forwarded_port", guest: 3389, host: 23389, id: "rdp", auto_correct: true`,
		`config.vagrant.plugins = ["vagrant-qemu"]`,
		"`which qemu-system-aarch64 2>/dev/null`",
		`qe.qemu_dir = qemu_dir if qemu_dir`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Vagrantfile missing %q\n%s", want, got)
		}
	}
}

func TestWriteVagrantfileCustomPort(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "Vagrantfile")
	if err := WriteVagrantfile(dest, VagrantOpts{
		ImageName: "disk.qcow2", SSHPort: 2299,
	}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dest)
	if !strings.Contains(string(data), `qe.ssh_port = Integer(ENV.fetch("WINKIT_SSH_PORT", 2299))`) {
		t.Errorf("custom ssh port not rendered:\n%s", data)
	}
}

func TestWriteVagrantfileRequiresImage(t *testing.T) {
	if err := WriteVagrantfile(filepath.Join(t.TempDir(), "Vagrantfile"), VagrantOpts{}); err == nil {
		t.Fatal("expected error for missing ImageName")
	}
}

func TestWriteVagrantfileHostname(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "Vagrantfile")
	if err := WriteVagrantfile(dest, VagrantOpts{ImageName: "d.qcow2", Hostname: "dev-win11"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dest)
	if !strings.Contains(string(data), `"-smbios", "type=1,serial=dev-win11",`) {
		t.Errorf("smbios serial not rendered:\n%s", data)
	}
}

func TestWriteBoxVagrantfile(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "Vagrantfile")
	if err := WriteBoxVagrantfile(dest, "winkit-base.box", "winkit/base"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	for _, want := range []string{
		`config.vm.box = "winkit/base"`,
		`config.vm.box_url = "file://" + File.expand_path("winkit-base.box", __dir__)`,
		`config.vagrant.plugins = ["vagrant-qemu"]`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("box Vagrantfile missing %q\n%s", want, got)
		}
	}
}
