package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/mctcatalog"
	"github.com/devcell-sh/go-winkit/uupdump"
)

func newFetchCmd() *cobra.Command {
	var (
		source      string
		cacheDir    string
		language    string
		edition     string
		concurrency int
	)

	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "Download a Windows ARM64 build and assemble a bootable ISO",
		Long: "Downloads a Windows ARM64 build and assembles a bootable ISO,\n" +
			"caching the result. Prints the ISO path on success.\n\n" +
			"Sources: uupdump (UUP dump API) or mct (Microsoft Update Catalog).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cacheDir == "" {
				base, err := os.UserCacheDir()
				if err != nil {
					return fmt.Errorf("resolving cache dir: %w", err)
				}
				cacheDir = filepath.Join(base, "winkit")
			}

			logf := func(format string, a ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...)
			}
			progress := func(filename string, downloaded, total int64) {
				if total > 0 {
					fmt.Fprintf(cmd.ErrOrStderr(), "\r%s: %.0f / %.0f MB (%.1f%%)",
						filename,
						float64(downloaded)/(1024*1024),
						float64(total)/(1024*1024),
						float64(downloaded)/float64(total)*100)
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
					Language:    language,
					Edition:     edition,
					Concurrency: concurrency,
					LogFunc:     logf,
					OnProgress:  progress,
				})
			case "mct":
				isoPath, err = mctcatalog.FetchWindowsISO(cmd.Context(), mctcatalog.FetchConfig{
					CacheDir:   cacheDir,
					Language:   language,
					Edition:    edition,
					LogFunc:    logf,
					OnProgress: progress,
				})
			default:
				return fmt.Errorf("unknown source %q (want uupdump or mct)", source)
			}
			fmt.Fprintln(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), isoPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&source, "source", "uupdump", "media source: uupdump or mct")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "cache directory (default: user cache dir + /winkit)")
	cmd.Flags().StringVar(&language, "lang", "en-us", "language code")
	cmd.Flags().StringVar(&edition, "edition", "PROFESSIONAL", "Windows edition")
	cmd.Flags().IntVar(&concurrency, "concurrency", 5, "parallel downloads (uupdump only)")
	return cmd
}
