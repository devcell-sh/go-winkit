//go:build !windows

package main

import "fmt"

func enumTopLevelWindows() []WindowInfo { return nil }
func activateWindow(uintptr)            {}
func launchProcess(path string) error   { return fmt.Errorf("not supported on this platform") }
func registerAsShell(uintptr) error     { return fmt.Errorf("not supported on this platform") }
func platformScreenSize() (int, int)    { return 0, 0 }
