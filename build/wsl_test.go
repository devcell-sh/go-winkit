package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcell-sh/go-winkit/unattend"
)

// TestWSLAnswerConfig asserts the offline (Phase 0) answer config for the
// wsl stage: RDP + OpenSSH payload + the netkvm/vioserial drivers registered
// in specialize, and that it renders a schema-valid autounattend.
func TestWSLAnswerConfig(t *testing.T) {
	pwsh := map[string][]byte{"/pwsh/pwsh.exe": []byte("MZ")}
	cfg := wslAnswerConfig(pwsh, "openssh-arm64.zip", []byte("PK\x03\x04zip"), "gosshd.exe", []byte("MZgosshd"))

	if !cfg.EnableRDP {
		t.Error("wsl config must enable RDP")
	}
	if cfg.OpenSSHPayload != "openssh-arm64.zip" || cfg.OpenSSHPayloadSize != len("PK\x03\x04zip") {
		t.Errorf("OpenSSH payload not wired: name=%q size=%d", cfg.OpenSSHPayload, cfg.OpenSSHPayloadSize)
	}
	if cfg.PwshFiles["/pwsh/pwsh.exe"] == nil {
		t.Error("pwsh files not carried")
	}

	// gosshd provisioning server: binary carried, on a non-standard port, and
	// the display-keep-alive is on for headless install visibility.
	if cfg.GosshdBinaryName != "gosshd.exe" || len(cfg.GosshdBinaryData) == 0 {
		t.Errorf("gosshd binary not wired: name=%q size=%d", cfg.GosshdBinaryName, len(cfg.GosshdBinaryData))
	}
	if cfg.GosshdListenAddrOrDefault() == ":22" {
		t.Error("gosshd must not use :22 (reserved for the delivered Windows OpenSSH)")
	}
	if !cfg.KeepDisplayAwake {
		t.Error("wsl config should keep the display awake during install")
	}

	// Drivers: netkvm (network, mandatory) + vioserial (build.jsonl), both
	// installed via pnputil in specialize.
	var haveNet, haveSerial bool
	for _, d := range cfg.VirtIODrivers {
		if strings.Contains(d.INFRelPath, "netkvm.inf") {
			haveNet = true
		}
		if strings.Contains(d.INFRelPath, "vioser.inf") {
			haveSerial = true
		}
	}
	if !haveNet {
		t.Error("wsl config missing NetKVM driver (network → SSH/RDP)")
	}
	if !haveSerial {
		t.Error("wsl config missing vioserial driver (build.jsonl)")
	}

	// The rendered autounattend must pass the package's own schema validation.
	xml := unattend.GenerateXML(cfg)
	if len(xml) == 0 {
		t.Fatal("GenerateXML returned empty")
	}
	if errs := unattend.Validate(xml); len(errs) > 0 {
		t.Fatalf("wsl autounattend failed schema validation: %v", errs)
	}
}

// TestWSLAnswerVolumeBuilds runs the full offline Phase-0 answer-volume build
// end to end (no VM), confirming the wsl config produces a real FAT image.
func TestWSLAnswerVolumeBuilds(t *testing.T) {
	cfg := wslAnswerConfig(nil, "openssh-arm64.zip", []byte("PK\x03\x04zip"), "gosshd.exe", []byte("MZgosshd"))
	dest := filepath.Join(t.TempDir(), "autounattend.img")
	if err := unattend.BuildAnswerVolume(cfg, dest); err != nil {
		t.Fatalf("BuildAnswerVolume: %v", err)
	}
	fi, err := os.Stat(dest)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("answer volume not written: err=%v size=%d", err, fi.Size())
	}
}
