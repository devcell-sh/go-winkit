//go:build !windows

package main

import "fmt"

func spawnAsUser(user, password, exe string, args []string) error {
	return fmt.Errorf("init: spawnAsUser is only supported on Windows")
}

func spawnAsUserAndWait(user, password, exe string, args []string) error {
	return fmt.Errorf("init: spawnAsUserAndWait is only supported on Windows")
}
