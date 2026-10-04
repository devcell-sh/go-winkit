//go:build !windows

package main

import "fmt"

func grantWindowStationAccess(string) error {
	return fmt.Errorf("window station access grants are only supported on Windows")
}
