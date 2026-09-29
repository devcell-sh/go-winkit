//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// spawnAsUser starts a detached process under the given user's context.
// The child inherits no console and runs independently.
func spawnAsUser(user, password, exe string, args []string) error {
	token, profile, err := logonAndLoadProfile(user, password)
	if err != nil {
		return err
	}
	// Intentionally not unloading profile or closing token: the child
	// needs them for its entire lifetime, and init never exits.

	var environment *uint16
	if err := windows.CreateEnvironmentBlock(&environment, token, true); err != nil {
		return fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	_ = profile // keep reference alive

	cmdLine := []string{exe}
	cmdLine = append(cmdLine, args...)
	cmdLineStr, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmdLine))
	if err != nil {
		return err
	}

	startup := windows.StartupInfo{
		Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})),
	}
	var process windows.ProcessInformation
	if err := windows.CreateProcessAsUser(
		token, nil, cmdLineStr, nil, nil, false,
		windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NEW_PROCESS_GROUP,
		environment, nil, &startup, &process,
	); err != nil {
		return fmt.Errorf("CreateProcessAsUser %s: %w", exe, err)
	}
	windows.CloseHandle(process.Thread)
	windows.CloseHandle(process.Process)
	return nil
}

// spawnAsUserAndWait starts a process under the given user's context
// and waits for it to complete.
func spawnAsUserAndWait(user, password, exe string, args []string) error {
	token, profile, err := logonAndLoadProfile(user, password)
	if err != nil {
		return err
	}
	_ = profile

	var environment *uint16
	if err := windows.CreateEnvironmentBlock(&environment, token, true); err != nil {
		return fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}

	cmdLine := []string{exe}
	cmdLine = append(cmdLine, args...)
	cmdLineStr, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(cmdLine))
	if err != nil {
		return err
	}

	startup := windows.StartupInfo{
		Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})),
	}
	var process windows.ProcessInformation
	if err := windows.CreateProcessAsUser(
		token, nil, cmdLineStr, nil, nil, false,
		windows.CREATE_UNICODE_ENVIRONMENT, environment, nil, &startup, &process,
	); err != nil {
		return fmt.Errorf("CreateProcessAsUser %s: %w", exe, err)
	}
	defer windows.CloseHandle(process.Thread)
	defer windows.CloseHandle(process.Process)

	if _, err := windows.WaitForSingleObject(process.Process, windows.INFINITE); err != nil {
		return fmt.Errorf("WaitForSingleObject: %w", err)
	}

	var exitCode uint32
	if err := windows.GetExitCodeProcess(process.Process, &exitCode); err != nil {
		return fmt.Errorf("GetExitCodeProcess: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("%s exited with code %d", exe, exitCode)
	}
	return nil
}

func logonAndLoadProfile(user, password string) (windows.Token, runUserProfileInfo, error) {
	userPtr, err := windows.UTF16PtrFromString(user)
	if err != nil {
		return 0, runUserProfileInfo{}, err
	}
	domainPtr, _ := windows.UTF16PtrFromString(".")
	passwordPtr, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return 0, runUserProfileInfo{}, err
	}

	var token windows.Token
	r1, _, callErr := runUserLogonUserW.Call(
		uintptr(unsafe.Pointer(userPtr)),
		uintptr(unsafe.Pointer(domainPtr)),
		uintptr(unsafe.Pointer(passwordPtr)),
		2, // LOGON32_LOGON_INTERACTIVE
		0, // LOGON32_PROVIDER_DEFAULT
		uintptr(unsafe.Pointer(&token)),
	)
	if r1 == 0 {
		return 0, runUserProfileInfo{}, fmt.Errorf("LogonUserW: %w", callErr)
	}

	profile := runUserProfileInfo{
		size:     uint32(unsafe.Sizeof(runUserProfileInfo{})),
		userName: userPtr,
	}
	r1, _, callErr = runUserLoadUserProfileW.Call(
		uintptr(token), uintptr(unsafe.Pointer(&profile)))
	if r1 == 0 {
		// Non-fatal: profile may not be ready yet in early WinPE.
	}

	return token, profile, nil
}
