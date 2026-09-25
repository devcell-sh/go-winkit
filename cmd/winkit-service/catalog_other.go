//go:build !windows

package main

import "fmt"

func addCatalogs(string) (int, error) {
	return 0, fmt.Errorf("catalog registration is only supported on Windows")
}

func findCatalog(string) (string, error) {
	return "", fmt.Errorf("catalog lookup is only supported on Windows")
}
