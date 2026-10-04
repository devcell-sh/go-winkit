//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32              = windows.NewLazySystemDLL("user32.dll")
	procOpenWindowStationW = modUser32.NewProc("OpenWindowStationW")
	procOpenDesktopW       = modUser32.NewProc("OpenDesktopW")
	procCloseWindowStation = modUser32.NewProc("CloseWindowStation")
	procCloseDesktop       = modUser32.NewProc("CloseDesktop")
)

// grantWindowStationAccess adds a full-access ACE for account to the
// interactive window station (WinSta0) and its Default desktop.
//
// init spawns gosshd, pe-agent and the WSL1 s6 wrapper as a local user via
// CreateProcessAsUser. Those processes land on WinSta0 because init (the
// WinPE shell, SYSTEM) is there, but WinSta0's DACL only names SYSTEM, the
// font and DWM service SIDs and the logon SID of an interactive logon
// that never happened. The result is a GUI that half works: windows are
// created and their frames drawn by win32k, but the process cannot get a
// screen DC, so client areas never paint and GetDC(NULL) fails with
// "handle is invalid". Granting the user explicit access is what winlogon
// does for a real interactive logon.
func grantWindowStationAccess(account string) error {
	sid, _, _, err := windows.LookupSID("", account)
	if err != nil {
		return fmt.Errorf("lookup %s: %w", account, err)
	}

	const access = windows.READ_CONTROL | windows.WRITE_DAC
	ws, err := openWindowStation("WinSta0", access)
	if err != nil {
		return fmt.Errorf("open WinSta0: %w", err)
	}
	defer procCloseWindowStation.Call(uintptr(ws))
	if err := grantFullAccess(ws, sid); err != nil {
		return fmt.Errorf("WinSta0: %w", err)
	}

	dk, err := openDesktop("Default", access)
	if err != nil {
		return fmt.Errorf("open Default desktop: %w", err)
	}
	defer procCloseDesktop.Call(uintptr(dk))
	if err := grantFullAccess(dk, sid); err != nil {
		return fmt.Errorf("Default desktop: %w", err)
	}
	return nil
}

// grantFullAccess merges a GENERIC_ALL grant for sid into the object's
// existing DACL. Existing ACEs are kept so SYSTEM and the service SIDs
// retain what they had.
func grantFullAccess(h windows.Handle, sid *windows.SID) error {
	sd, err := windows.GetSecurityInfo(h, windows.SE_WINDOW_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("get DACL: %w", err)
	}
	old, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read DACL: %w", err)
	}
	entries := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}
	acl, err := windows.ACLFromEntries(entries, old)
	if err != nil {
		return fmt.Errorf("merge DACL: %w", err)
	}
	if err := windows.SetSecurityInfo(h, windows.SE_WINDOW_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("set DACL: %w", err)
	}
	return nil
}

func openWindowStation(name string, access uint32) (windows.Handle, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	h, _, e := procOpenWindowStationW.Call(uintptr(unsafe.Pointer(n)), 0, uintptr(access))
	if h == 0 {
		return 0, e
	}
	return windows.Handle(h), nil
}

func openDesktop(name string, access uint32) (windows.Handle, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	h, _, e := procOpenDesktopW.Call(uintptr(unsafe.Pointer(n)), 0, 0, uintptr(access))
	if h == 0 {
		return 0, e
	}
	return windows.Handle(h), nil
}
