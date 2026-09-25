//go:build !windows

package main

import (
	"fmt"
	"time"
)

func ensureService(string, time.Duration) error {
	return fmt.Errorf("ensure-service is only supported on Windows")
}
