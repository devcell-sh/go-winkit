//go:build !windows

package main

import "fmt"

func ensureLocalUser(string, string) error {
	return fmt.Errorf("ensure-user is only supported on Windows")
}

func addToAdministrators(string) error {
	return fmt.Errorf("ensure-user --admin is only supported on Windows")
}
