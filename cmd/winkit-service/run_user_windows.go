//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const runAsUserTimeoutMS = 10 * 60 * 1000

var (
	runUserAdvapi32          = windows.NewLazySystemDLL("advapi32.dll")
	runUserUserenv           = windows.NewLazySystemDLL("userenv.dll")
	runUserLogonUserW        = runUserAdvapi32.NewProc("LogonUserW")
	runUserLoadUserProfileW  = runUserUserenv.NewProc("LoadUserProfileW")
	runUserUnloadUserProfile = runUserUserenv.NewProc("UnloadUserProfile")
)

type runUserProfileInfo struct {
	size          uint32
	flags         uint32
	userName      *uint16
	profilePath   *uint16
	defaultPath   *uint16
	serverName    *uint16
	policyPath    *uint16
	profileHandle windows.Handle
}

// runAsUser creates an interactive logon, loads that user's HKCU/profile,
// supplies a matching environment block, and waits for the command. WSL
// registrations and COM sessions are per-user, so merely changing the SCM
// service account is not equivalent in WinPE.
func runAsUser(user, password, currentDir string, command []string) (uint32, error) {
	userPtr, err := windows.UTF16PtrFromString(user)
	if err != nil {
		return 0, err
	}
	domainPtr, _ := windows.UTF16PtrFromString(".")
	passwordPtr, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return 0, err
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
		return 0, fmt.Errorf("LogonUserW: %w", callErr)
	}
	defer token.Close()

	profile := runUserProfileInfo{
		size:     uint32(unsafe.Sizeof(runUserProfileInfo{})),
		userName: userPtr,
	}
	r1, _, callErr = runUserLoadUserProfileW.Call(
		uintptr(token), uintptr(unsafe.Pointer(&profile)))
	profileLoaded := r1 != 0
	if !profileLoaded && !errors.Is(callErr, windows.ERROR_NOT_READY) {
		return 0, fmt.Errorf("LoadUserProfileW: %w", callErr)
	}
	if profileLoaded {
		defer runUserUnloadUserProfile.Call(uintptr(token), uintptr(profile.profileHandle))
	}

	var environment *uint16
	if err := windows.CreateEnvironmentBlock(&environment, token, false); err != nil {
		return 0, fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(environment)

	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(command))
	if err != nil {
		return 0, err
	}
	var currentDirPtr *uint16
	if currentDir != "" {
		currentDirPtr, err = windows.UTF16PtrFromString(currentDir)
		if err != nil {
			return 0, err
		}
	}

	stdin, err := duplicateInheritableHandle(windows.Handle(os.Stdin.Fd()))
	if err != nil {
		return 0, fmt.Errorf("duplicating stdin: %w", err)
	}
	defer windows.CloseHandle(stdin)
	stdout, err := duplicateInheritableHandle(windows.Handle(os.Stdout.Fd()))
	if err != nil {
		return 0, fmt.Errorf("duplicating stdout: %w", err)
	}
	defer windows.CloseHandle(stdout)
	stderr, err := duplicateInheritableHandle(windows.Handle(os.Stderr.Fd()))
	if err != nil {
		return 0, fmt.Errorf("duplicating stderr: %w", err)
	}
	defer windows.CloseHandle(stderr)

	startup := windows.StartupInfo{
		Cb:        uint32(unsafe.Sizeof(windows.StartupInfo{})),
		Flags:     windows.STARTF_USESTDHANDLES,
		StdInput:  stdin,
		StdOutput: stdout,
		StdErr:    stderr,
	}
	var process windows.ProcessInformation
	if err := windows.CreateProcessAsUser(
		token, nil, commandLine, nil, nil, true,
		windows.CREATE_UNICODE_ENVIRONMENT, environment, currentDirPtr,
		&startup, &process,
	); err != nil {
		return 0, fmt.Errorf("CreateProcessAsUserW: %w", err)
	}
	defer windows.CloseHandle(process.Thread)
	defer windows.CloseHandle(process.Process)

	wait, err := windows.WaitForSingleObject(process.Process, runAsUserTimeoutMS)
	if err != nil {
		return 0, fmt.Errorf("WaitForSingleObject: %w", err)
	}
	if wait == uint32(windows.WAIT_TIMEOUT) {
		_ = windows.TerminateProcess(process.Process, 1460)
		return 1460, fmt.Errorf("command timed out after 10 minutes")
	}

	var exitCode uint32
	if err := windows.GetExitCodeProcess(process.Process, &exitCode); err != nil {
		return 0, fmt.Errorf("GetExitCodeProcess: %w", err)
	}
	return exitCode, nil
}

func duplicateInheritableHandle(source windows.Handle) (windows.Handle, error) {
	process := windows.CurrentProcess()
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(
		process,
		source,
		process,
		&duplicate,
		0,
		true,
		windows.DUPLICATE_SAME_ACCESS,
	); err != nil {
		return 0, err
	}
	return duplicate, nil
}
