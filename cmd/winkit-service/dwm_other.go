//go:build !windows

package main

import (
	"fmt"
	"io"
)

func startDWM(_ io.Writer) error {
	return fmt.Errorf("DWM is only available on Windows")
}
