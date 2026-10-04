//go:build !windows

package main

import "fmt"

func grantServiceLogonRight(string) error {
	return fmt.Errorf("granting service logon rights is only supported on Windows")
}
