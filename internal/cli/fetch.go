package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/media/mctcatalog"
	"github.com/devcell-sh/go-winkit/media/uupdump"
	"github.com/devcell-sh/go-winkit/media/virtio"
)

func newFetchCmd() *cobra.Command {
	var (
		source             string
		cacheDir           string
		spec               uupdump.MediaSpec
		concurrency        int
		bootCompression    string
		installCompression string
		reuseBootWim       bool
	)

	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "Download a Windows ARM64 build and assemble a bootable ISO",
		Long: "Downloads a Windows ARM64 build and assembles a bootable ISO,\n" +
			"caching the result. Prints the ISO path on success.\n\n" +
			"Sources: uupdump (UUP dump API) or mct (Microsoft Update Catalog).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// ISO assembly needs libwim — fail before the multi-GB
			// download, not after it.
			if err := requireWimlib(); err != nil {
				return err
			}
			if cacheDir == "" {
				cacheDir = cache.Dir()
			}

			// A --build pin is incompatible with the mct source: MCT
			// serves only the currently published GA build.
			hasBuildPin := spec.Build != ""
			sourceExplicit := cmd.Flags().Changed("source")
			if hasBuildPin && source == "mct" {
				if sourceExplicit {
					return fmt.Errorf("--source mct cannot be combined with --build: "+
						"the MCT catalog serves only the published GA build; "+
						"use --source uupdump --build %s instead", spec.Build)
				}
				// Implicit default: silently re-route to uupdump.
				source = "uupdump"
			}

			ui := newRunUI(cmd)
			logger := ui.Logger
			progress := func(filename string, downloaded, total int64) {
				if total > 0 {
					ui.Progress(fmt.Sprintf("%s: %.0f / %.0f MB (%.1f%%)",
						filename,
						float64(downloaded)/(1024*1024),
						float64(total)/(1024*1024),
						float64(downloaded)/float64(total)*100))
				}
			}

			var (
				isoPath string
				err     error
			)
			switch source {
			case "uupdump":
				isoPath, err = uupdump.FetchWindowsISO(cmd.Context(), uupdump.FetchConfig{
					CacheDir:    cacheDir,
					Spec:        spec,
					Concurrency: concurrency,
					Logger:      logger,
					OnProgress:  progress,
					OnFileStart: func(name string) { ui.ItemStart(itemName(name)) },
					OnFileDone:  func(name string, _ int64) { ui.ItemDone(itemName(name)) },

					BootWimCompression:    compressionFlag(bootCompression),
					InstallWimCompression: compressionFlag(installCompression),
					ReuseBootWim:          reuseBootWim,
				})
			case "mct":
				isoPath, err = mctcatalog.FetchWindowsISO(cmd.Context(), mctcatalog.FetchConfig{
					CacheDir: cacheDir,
					Language: spec.Language,
					Edition:  spec.Edition,
					LogFunc: func(format string, a ...any) {
						logger.Info(fmt.Sprintf(format, a...))
					},
					OnProgress: progress,
				})
			default:
				return fmt.Errorf("unknown source %q (want uupdump or mct)", source)
			}
			ui.Finish(err)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), isoPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&source, "source", "mct", "media source: mct (default, complete media) or uupdump (pinned builds)")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "cache directory (default: "+cache.DirEnv+", else user cache dir + /winkit)")
	addMediaSpecFlags(cmd, &spec)
	cmd.Flags().IntVar(&concurrency, "concurrency", 5, "parallel downloads (uupdump only)")
	cmd.Flags().StringVar(&bootCompression, "boot-wim-compression", "",
		"boot.wim compression: none, lzx or lzms (default: lzx, as shipping media uses)")
	cmd.Flags().StringVar(&installCompression, "install-wim-compression", "",
		"install.wim compression: none, lzx or lzms (default: lzms, as shipping media uses)")
	cmd.Flags().BoolVar(&reuseBootWim, "reuse-boot-wim", false,
		"reuse an existing boot.wim instead of rebuilding it (for iterating on later stages; "+
			"the reused artifact is NOT rebuilt from the current ESD or code)")

	cmd.AddCommand(newFetchVirtIOCmd())
	return cmd
}

// compressionFlag maps the flag spelling onto the config sentinel. An
// unrecognised value falls through to the default rather than failing,
// since the zero value means "what shipping media uses".
func compressionFlag(name string) uupdump.Compression {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "none":
		return uupdump.CompressionNone
	case "lzx":
		return uupdump.CompressionLZX
	case "lzms":
		return uupdump.CompressionLZMS
	default:
		return uupdump.CompressionDefault
	}
}

func newFetchVirtIOCmd() *cobra.Command {
	var (
		cacheDir string
		url      string
	)

	cmd := &cobra.Command{
		Use:   "virtio",
		Short: "Download the virtio-win driver ISO",
		Long: "Downloads the virtio-win driver ISO, the source of the storage,\n" +
			"serial and network drivers WinPE needs to see a QEMU guest's\n" +
			"devices. Caches the result and prints the ISO path on success.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cacheDir == "" {
				cacheDir = cache.Dir()
			}

			ui := newRunUI(cmd)
			isoPath, err := virtio.FetchISO(cmd.Context(), virtio.FetchConfig{
				CacheDir: cacheDir,
				URL:      url,
				Logger:   ui.Logger,
				OnProgress: func(downloaded, total int64) {
					if total > 0 {
						ui.Progress(fmt.Sprintf("virtio-win.iso: %.0f / %.0f MB (%.1f%%)",
							float64(downloaded)/(1024*1024),
							float64(total)/(1024*1024),
							float64(downloaded)/float64(total)*100))
					}
				},
			})
			ui.Finish(err)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), isoPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "cache directory (default: "+cache.DirEnv+", else user cache dir + /winkit)")
	cmd.Flags().StringVar(&url, "url", "", "override the download URL (default: upstream stable channel)")
	return cmd
}
