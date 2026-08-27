// Command winkit is the host-side CLI over the go-winkit packages: fetch
// Windows install media, build bootable and WinPE ISOs, generate and validate
// autounattend answer files, patch WIMs, and read guest diagnostics.
package main

import (
	"os"

	"github.com/devcell-sh/go-winkit/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
