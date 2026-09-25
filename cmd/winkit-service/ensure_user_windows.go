//go:build windows

package main

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	userPrivilegeUser   = 1
	userFlagScript      = 0x0001
	netAPIStatusSuccess = 0
	netErrUserExists    = 2224
)

var (
	netapi32   = windows.NewLazySystemDLL("netapi32.dll")
	netUserAdd = netapi32.NewProc("NetUserAdd")
)

type userInfo1 struct {
	name        *uint16
	password    *uint16
	passwordAge uint32
	privilege   uint32
	homeDir     *uint16
	comment     *uint16
	flags       uint32
	scriptPath  *uint16
}

var (
	netLocalGroupAddMembers = netapi32.NewProc("NetLocalGroupAddMembers")
)

type localgroupMembersInfo3 struct {
	domainAndName *uint16
}

// ensureLocalUser creates a local account directly through NetUserAdd. Unlike
// `net user /add`, this does not try to add the account to a localized default
// group, which stock WinPE does not provide.
func ensureLocalUser(user, password string) error {
	userPtr, err := windows.UTF16PtrFromString(user)
	if err != nil {
		return err
	}
	passwordPtr, err := windows.UTF16PtrFromString(password)
	if err != nil {
		return err
	}
	info := userInfo1{
		name:      userPtr,
		password:  passwordPtr,
		privilege: userPrivilegeUser,
		flags:     userFlagScript,
	}
	var parameterError uint32
	status, _, _ := netUserAdd.Call(
		0,
		1,
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Pointer(&parameterError)),
	)
	runtime.KeepAlive(userPtr)
	runtime.KeepAlive(passwordPtr)
	runtime.KeepAlive(info)
	if status == netAPIStatusSuccess || status == netErrUserExists {
		return nil
	}
	return fmt.Errorf("NetUserAdd(%s) failed at parameter %d: %w", user, parameterError, syscall.Errno(status))
}

const netErrMemberInAlias = 1378

func addToAdministrators(user string) error {
	groupPtr, err := windows.UTF16PtrFromString("Administrators")
	if err != nil {
		return err
	}
	memberPtr, err := windows.UTF16PtrFromString(user)
	if err != nil {
		return err
	}
	member := localgroupMembersInfo3{domainAndName: memberPtr}
	status, _, _ := netLocalGroupAddMembers.Call(
		0,
		uintptr(unsafe.Pointer(groupPtr)),
		3,
		uintptr(unsafe.Pointer(&member)),
		1,
	)
	runtime.KeepAlive(groupPtr)
	runtime.KeepAlive(memberPtr)
	runtime.KeepAlive(member)
	if status == netAPIStatusSuccess || status == netErrMemberInAlias {
		return nil
	}
	return fmt.Errorf("NetLocalGroupAddMembers(Administrators, %s): %w", user, syscall.Errno(status))
}
