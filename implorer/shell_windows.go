package main

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	shell32                  = windows.NewLazySystemDLL("shell32.dll")
	procEnumWindows          = user32.NewProc("EnumWindows")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procIsWindowVisible      = user32.NewProc("IsWindowVisible")
	procSetShellWindow       = user32.NewProc("SetShellWindow")
	procRegisterShellHookWnd = user32.NewProc("RegisterShellHookWindow")
	procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	procShowWindow           = user32.NewProc("ShowWindow")
	procGetSystemMetrics     = user32.NewProc("GetSystemMetrics")
)

func platformScreenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
	h, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
	return int(w), int(h)
}

func enumTopLevelWindows() []WindowInfo {
	var result []WindowInfo
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		buf := make([]uint16, 256)
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
		title := syscall.UTF16ToString(buf)
		if title != "" {
			result = append(result, WindowInfo{HWND: hwnd, Title: title})
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return result
}

func activateWindow(hwnd uintptr) {
	procSetForegroundWindow.Call(hwnd)
	procShowWindow.Call(hwnd, 9) // SW_RESTORE
}

func launchProcess(path string) error {
	cmd := exec.Command(path)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd.Start()
}

func registerAsShell(hwnd uintptr) error {
	r, _, err := procSetShellWindow.Call(hwnd)
	if r == 0 {
		return fmt.Errorf("SetShellWindow: %w", err)
	}
	procRegisterShellHookWnd.Call(hwnd)
	return nil
}
