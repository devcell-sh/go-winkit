//go:build windows

package main

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SCM starts a service under a local account only if that account holds
// SeServiceLogonRight. Nothing grants it by default: not NetUserAdd, not
// kardianos/service, and WinPE has no secedit.exe. Without it `start`
// fails with ERROR_SERVICE_LOGON_FAILED (1069), which is how the PE
// winkit-s6 service died on first boot. LsaAddAccountRights is the API
// secedit uses underneath and is present in WinPE's advapi32.

var (
	lsaAdvapi32           = windows.NewLazySystemDLL("advapi32.dll")
	lsaOpenPolicy         = lsaAdvapi32.NewProc("LsaOpenPolicy")
	lsaAddAccountRights   = lsaAdvapi32.NewProc("LsaAddAccountRights")
	lsaClose              = lsaAdvapi32.NewProc("LsaClose")
	lsaNtStatusToWinError = lsaAdvapi32.NewProc("LsaNtStatusToWinError")
	policyCreateAccount   = uint32(0x00000010)
	policyLookupNames     = uint32(0x00000800)
	seServiceLogonRight   = "SeServiceLogonRight"
)

type lsaUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *uint16
}

type lsaObjectAttributes struct {
	Length                   uint32
	RootDirectory            windows.Handle
	ObjectName               *lsaUnicodeString
	Attributes               uint32
	SecurityDescriptor       uintptr
	SecurityQualityOfService uintptr
}

func newLSAString(s string) (lsaUnicodeString, error) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return lsaUnicodeString{}, err
	}
	// Length excludes the terminating NUL, MaximumLength includes it; both in bytes.
	n := uint16((len(u) - 1) * 2)
	return lsaUnicodeString{Length: n, MaximumLength: n + 2, Buffer: &u[0]}, nil
}

func lsaErr(status uintptr) error {
	code, _, _ := lsaNtStatusToWinError.Call(status)
	return syscall.Errno(code)
}

// grantServiceLogonRight gives the local account SeServiceLogonRight.
// Accepts the ".\user" form the service installer uses. Idempotent: the
// LSA call succeeds when the right is already held.
func grantServiceLogonRight(account string) error {
	account = strings.TrimPrefix(account, `.\`)
	if account == "" {
		return fmt.Errorf("empty account name")
	}
	sid, _, _, err := windows.LookupSID("", account)
	if err != nil {
		return fmt.Errorf("LookupAccountName(%s): %w", account, err)
	}

	var policy windows.Handle
	attrs := lsaObjectAttributes{Length: uint32(unsafe.Sizeof(lsaObjectAttributes{}))}
	status, _, _ := lsaOpenPolicy.Call(
		0, // local system
		uintptr(unsafe.Pointer(&attrs)),
		uintptr(policyCreateAccount|policyLookupNames),
		uintptr(unsafe.Pointer(&policy)),
	)
	if status != 0 {
		return fmt.Errorf("LsaOpenPolicy: %w", lsaErr(status))
	}
	defer lsaClose.Call(uintptr(policy))

	right, err := newLSAString(seServiceLogonRight)
	if err != nil {
		return err
	}
	status, _, _ = lsaAddAccountRights.Call(
		uintptr(policy),
		uintptr(unsafe.Pointer(sid)),
		uintptr(unsafe.Pointer(&right)),
		1,
	)
	if status != 0 {
		return fmt.Errorf("LsaAddAccountRights(%s, %s): %w", account, seServiceLogonRight, lsaErr(status))
	}
	return nil
}
