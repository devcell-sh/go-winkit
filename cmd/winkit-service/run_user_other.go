//go:build !windows

package main

import "fmt"

func runAsUser(user, password, currentDir string, command []string) (uint32, error) {
	return 0, fmt.Errorf("run-user is only supported on Windows")
}
