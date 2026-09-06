// Command seedstatus prints the cache paths the real-media tests read and
// whether each one is seeded.
//
// It resolves the paths through the same package the tests and `winkit
// fetch` use, so it cannot drift from them the way a hardcoded shell
// equivalent would.
package main

import (
	"fmt"
	"os"

	"github.com/devcell-sh/go-winkit/cache"
)

func main() {
	fmt.Printf("cache dir: %s\n", cache.Dir())
	if d := os.Getenv(cache.DirEnv); d != "" {
		fmt.Printf("           (from %s)\n", cache.DirEnv)
	} else {
		fmt.Printf("           (default; override with %s)\n", cache.DirEnv)
	}
	fmt.Println()

	seeded := true
	for _, artifact := range []struct {
		label string
		path  string
	}{
		{"windows installer", cache.WindowsISO()},
		{"virtio-win drivers", cache.VirtIOISO()},
	} {
		info, err := os.Stat(artifact.path)
		switch {
		case err != nil:
			seeded = false
			fmt.Printf("  MISSING  %-19s %s\n", artifact.label, artifact.path)
		default:
			fmt.Printf("  ok       %-19s %s (%.1f GB)\n",
				artifact.label, artifact.path,
				float64(info.Size())/(1024*1024*1024))
		}
	}

	if !seeded {
		fmt.Println("\nSeed the cache with: task test:seed")
		os.Exit(1)
	}
}
