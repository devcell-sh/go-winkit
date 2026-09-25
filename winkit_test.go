package winkit

import (
	"context"
	"strings"
	"testing"

	"github.com/devcell-sh/go-winkit/build"
	"github.com/devcell-sh/go-winkit/build/buildopts"
)

func TestBuild_AcceptsPEWSLAndDispatchesPEBuilder(t *testing.T) {
	err := Build(context.Background(), build.Config{
		Dest:       "out.qcow2",
		WindowsISO: "win.iso",
		VirtIOISO:  "virtio.iso",
		Opts: &buildopts.BuildOpts{
			PE:  true,
			WSL: &buildopts.WSLConfig{Image: "alpine"},
		},
	})
	if err == nil {
		t.Fatal("expected invalid-media error after PE+WSL validation")
	}
	if strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("PE+WSL must reach the standard PE builder, got %q", err)
	}
}

func TestBuild_RequiresDest(t *testing.T) {
	err := Build(context.Background(), build.Config{
		WindowsISO: "win.iso",
		VirtIOISO:  "virtio.iso",
	})
	if err == nil || !strings.Contains(err.Error(), "Dest") {
		t.Fatalf("expected Dest error, got %v", err)
	}
}

func TestBuild_RequiresMedia(t *testing.T) {
	err := Build(context.Background(), build.Config{Dest: "out.qcow2"})
	if err == nil || !strings.Contains(err.Error(), "WindowsISO") {
		t.Fatalf("expected WindowsISO error, got %v", err)
	}

	err = Build(context.Background(), build.Config{
		Dest:       "out.qcow2",
		WindowsISO: "win.iso",
	})
	if err == nil || !strings.Contains(err.Error(), "VirtIOISO") {
		t.Fatalf("expected VirtIOISO error, got %v", err)
	}
}

func TestBuild_LoadsServicesDir(t *testing.T) {
	// A library caller setting Opts.WSL.ServicesDir must get the same
	// treatment as the CLI: the dir is loaded into Config.WSLServices.
	// An invalid dir must fail the build up front.
	err := Build(context.Background(), build.Config{
		Dest:       t.TempDir() + "/out.qcow2",
		WindowsISO: "win.iso",
		VirtIOISO:  "virtio.iso",
		Opts: &buildopts.BuildOpts{
			WSL: &buildopts.WSLConfig{
				Image:       "alpine",
				ServicesDir: t.TempDir() + "/nonexistent",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "services") {
		t.Fatalf("expected services-dir error, got %v", err)
	}
}

func TestBuild_NilOptsGetsDefaults(t *testing.T) {
	// Nil Opts must not panic: defaults are applied and the dispatch
	// runs. The canceled context and temp cache keep the base-install
	// path from doing real work (payload downloads) before it errors.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Build(ctx, build.Config{
		Dest:       t.TempDir() + "/out.qcow2",
		CacheDir:   t.TempDir(),
		WindowsISO: "/nonexistent/win.iso",
		VirtIOISO:  "/nonexistent/virtio.iso",
	})
	if err == nil {
		t.Fatal("expected error for canceled build with nonexistent media")
	}
	if strings.Contains(err.Error(), "nil pointer") {
		t.Fatalf("nil Opts dereferenced: %v", err)
	}
}
