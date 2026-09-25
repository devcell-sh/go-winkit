//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wintrust                             = windows.NewLazySystemDLL("wintrust.dll")
	cryptCATAdminAcquireContext2         = wintrust.NewProc("CryptCATAdminAcquireContext2")
	cryptCATAdminAddCatalog              = wintrust.NewProc("CryptCATAdminAddCatalog")
	cryptCATAdminCalcHashFromFileHandle2 = wintrust.NewProc("CryptCATAdminCalcHashFromFileHandle2")
	cryptCATAdminEnumCatalogFromHash     = wintrust.NewProc("CryptCATAdminEnumCatalogFromHash")
	cryptCATCatalogInfoFromContext       = wintrust.NewProc("CryptCATCatalogInfoFromContext")
	cryptCATAdminReleaseCatalogContext   = wintrust.NewProc("CryptCATAdminReleaseCatalogContext")
	cryptCATAdminReleaseContext          = wintrust.NewProc("CryptCATAdminReleaseContext")
)

type catalogInfo struct {
	Size        uint32
	CatalogFile [windows.MAX_PATH]uint16
}

// driverActionVerify selects the driver catalog database used by Code
// Integrity. It is the DRIVER_ACTION_VERIFY subsystem GUID from Softpub.h.
var driverActionVerify = windows.GUID{
	Data1: 0x00aac56b,
	Data2: 0xcd44,
	Data3: 0x11d0,
	Data4: [8]byte{0x8c, 0xc2, 0x00, 0xc0, 0x4f, 0xc2, 0x95, 0xee},
}

func addCatalogs(dir string) (int, error) {
	catalogs, err := filepath.Glob(filepath.Join(dir, "*.cat"))
	if err != nil {
		return 0, fmt.Errorf("finding catalogs in %s: %w", dir, err)
	}
	if len(catalogs) == 0 {
		return 0, fmt.Errorf("no .cat files found in %s", dir)
	}
	sort.Strings(catalogs)

	admin, err := acquireCatalogContext()
	if err != nil {
		return 0, err
	}
	defer cryptCATAdminReleaseContext.Call(admin, 0)

	for i, catalog := range catalogs {
		catalogUTF16, convErr := windows.UTF16PtrFromString(catalog)
		if convErr != nil {
			return i, fmt.Errorf("encoding catalog path %s: %w", catalog, convErr)
		}
		catalogInfo, _, addErr := cryptCATAdminAddCatalog.Call(
			admin,
			uintptr(unsafe.Pointer(catalogUTF16)),
			0,
			0,
		)
		runtime.KeepAlive(catalogUTF16)
		if catalogInfo == 0 {
			return i, fmt.Errorf("adding catalog %s: %w", catalog,
				win32CallError("CryptCATAdminAddCatalog", addErr))
		}
		cryptCATAdminReleaseCatalogContext.Call(admin, catalogInfo, 0)
	}
	return len(catalogs), nil
}

func findCatalog(file string) (string, error) {
	fileUTF16, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return "", fmt.Errorf("encoding file path %s: %w", file, err)
	}
	h, err := windows.CreateFile(fileUTF16, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", file, err)
	}
	defer windows.CloseHandle(h)

	admin, err := acquireCatalogContext()
	if err != nil {
		return "", err
	}
	defer cryptCATAdminReleaseContext.Call(admin, 0)

	var hashSize uint32
	ok, _, callErr := cryptCATAdminCalcHashFromFileHandle2.Call(
		admin, uintptr(h), uintptr(unsafe.Pointer(&hashSize)), 0, 0)
	if ok == 0 {
		return "", win32CallError("CryptCATAdminCalcHashFromFileHandle2(size)", callErr)
	}
	hash := make([]byte, hashSize)
	ok, _, callErr = cryptCATAdminCalcHashFromFileHandle2.Call(
		admin, uintptr(h), uintptr(unsafe.Pointer(&hashSize)),
		uintptr(unsafe.Pointer(&hash[0])), 0)
	runtime.KeepAlive(hash)
	if ok == 0 {
		return "", win32CallError("CryptCATAdminCalcHashFromFileHandle2(hash)", callErr)
	}

	cat, _, enumErr := cryptCATAdminEnumCatalogFromHash.Call(
		admin, uintptr(unsafe.Pointer(&hash[0])), uintptr(hashSize), 0, 0)
	runtime.KeepAlive(hash)
	if cat == 0 {
		return "", win32CallError("CryptCATAdminEnumCatalogFromHash", enumErr)
	}
	defer cryptCATAdminReleaseCatalogContext.Call(admin, cat, 0)

	info := catalogInfo{Size: uint32(unsafe.Sizeof(catalogInfo{}))}
	ok, _, infoErr := cryptCATCatalogInfoFromContext.Call(
		cat, uintptr(unsafe.Pointer(&info)), 0)
	if ok == 0 {
		return "", win32CallError("CryptCATCatalogInfoFromContext", infoErr)
	}
	return windows.UTF16ToString(info.CatalogFile[:]), nil
}

func acquireCatalogContext() (uintptr, error) {
	algorithm, err := windows.UTF16PtrFromString("SHA256")
	if err != nil {
		return 0, err
	}
	var admin uintptr
	ok, _, callErr := cryptCATAdminAcquireContext2.Call(
		uintptr(unsafe.Pointer(&admin)),
		uintptr(unsafe.Pointer(&driverActionVerify)),
		uintptr(unsafe.Pointer(algorithm)),
		0,
		0,
	)
	runtime.KeepAlive(driverActionVerify)
	runtime.KeepAlive(algorithm)
	if ok == 0 {
		return 0, win32CallError("CryptCATAdminAcquireContext2(SHA256)", callErr)
	}
	return admin, nil
}

func win32CallError(op string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		return fmt.Errorf("%s failed", op)
	}
	return err
}
