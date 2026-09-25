// Command library-consumer is the devcell-shaped reference consumer of
// winkit's Go API: no winkit.yaml, everything programmatic. It compiles
// as part of the module, so any breaking change to the public surface
// fails CI here before it reaches a real embedder.
//
//	go run ./examples/library-consumer \
//	    -windows-iso ~/.cache/winkit/windows.iso \
//	    -virtio-iso  ~/.cache/winkit/virtio-win.iso \
//	    -dest ./winkit-wsl.qcow2
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	winkit "github.com/devcell-sh/go-winkit"
	"github.com/devcell-sh/go-winkit/build"
	"github.com/devcell-sh/go-winkit/build/buildopts"
)

func main() {
	var (
		windowsISO = flag.String("windows-iso", "", "path to the Windows installer ISO (required)")
		virtioISO  = flag.String("virtio-iso", "", "path to the virtio-win driver ISO (required)")
		dest       = flag.String("dest", "winkit-wsl.qcow2", "output image path")
		cacheDir   = flag.String("cache-dir", "", "media/rootfs cache dir")
		wslImage   = flag.String("wsl-image", "alpine", "WSL rootfs: docker ref, tarball URL, or .wsl path")
		boot       = flag.Bool("boot", false, "boot the image after building")
	)
	flag.Parse()
	if *windowsISO == "" || *virtioISO == "" {
		flag.Usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// The nouns: an opaque rootfs plus hooks at the phases we care
	// about. This is exactly how devcell supplies its nix flavor —
	// winkit never learns what is inside the image or the scripts.
	opts := &buildopts.BuildOpts{
		WSL: &buildopts.WSLConfig{Image: *wslImage},
		Hooks: []buildopts.Hook{
			{
				Phase: buildopts.Boot,
				Label: "greeting",
				Cmd:   `powershell -NoProfile -Command "Write-Host consumer-provisioned"`,
			},
			{
				Phase:   buildopts.WSLPhase,
				Label:   "distro-packages",
				Cmd:     "wsl -d winkit -- apk add htop",
				Timeout: 10 * time.Minute,
				Retries: 1,
			},
		},
	}

	err := winkit.Build(ctx, build.Config{
		Dest:       *dest,
		CacheDir:   *cacheDir,
		WindowsISO: *windowsISO,
		VirtIOISO:  *virtioISO,
		Logger:     logger,
		Opts:       opts,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.Exit(1)
	}
	fmt.Println("built", *dest)

	if !*boot {
		return
	}

	// The runtime half of the API: boot the image, print the SSH
	// address, wait for Ctrl+C. devcell's engine adapter wraps these
	// same calls.
	machine, err := winkit.Start(ctx, winkit.StartOpts{Image: *dest, Logger: logger})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start:", err)
		os.Exit(1)
	}
	fmt.Println("running, ssh:", machine.SSHAddr())

	select {
	case <-ctx.Done():
		fmt.Println("stopping")
		if err := machine.Stop(); err != nil {
			fmt.Fprintln(os.Stderr, "stop:", err)
		}
	case <-machine.Done():
		fmt.Println("vm exited")
	}
}
