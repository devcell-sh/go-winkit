package winpe

// WoW64System32Paths are the WoW64 subsystem files needed on ARM64
// WinPE to run x86 and x64 binaries. Without these, any non-ARM64
// executable fails with STATUS_DLL_NOT_FOUND (0xC0000135).
//
// xtajit64.dll (~30 MB) is the ARM64 JIT that translates x64 code;
// xtajit.dll handles x86. wow64.dll/wow64win.dll/wow64cpu.dll form
// the WoW64 thunking layer.
var WoW64System32Paths = []string{
	`\Windows\System32\wow64.dll`,
	`\Windows\System32\wow64win.dll`,
	`\Windows\System32\wow64cpu.dll`,
	`\Windows\System32\wow64con.dll`,
	`\Windows\System32\xtajit64.dll`,
	`\Windows\System32\xtajit.dll`,
	`\Windows\System32\xtac.exe`,
	`\Windows\System32\xtaofflinesetup.exe`,
	`\Windows\System32\arm64x\xtajit64.dll`,
	`\Windows\System32\arm64x\xtajit.dll`,
}

// WoW64SysWOW64Paths are files from the SysWOW64 directory (the x86/x64
// system root under WoW64). The entire SysWOW64 tree is large; we pull
// only the core loader and C runtime needed by most x64 applications.
var WoW64SysWOW64Paths = []string{
	`\Windows\SysWOW64\ntdll.dll`,
	`\Windows\SysWOW64\kernel32.dll`,
	`\Windows\SysWOW64\kernelbase.dll`,
	`\Windows\SysWOW64\ucrtbase.dll`,
	`\Windows\SysWOW64\msvcrt.dll`,
	`\Windows\SysWOW64\advapi32.dll`,
	`\Windows\SysWOW64\user32.dll`,
	`\Windows\SysWOW64\gdi32.dll`,
	`\Windows\SysWOW64\gdi32full.dll`,
	`\Windows\SysWOW64\shell32.dll`,
	`\Windows\SysWOW64\ole32.dll`,
	`\Windows\SysWOW64\oleaut32.dll`,
	`\Windows\SysWOW64\combase.dll`,
	`\Windows\SysWOW64\rpcrt4.dll`,
	`\Windows\SysWOW64\sechost.dll`,
	`\Windows\SysWOW64\bcrypt.dll`,
	`\Windows\SysWOW64\bcryptprimitives.dll`,
	`\Windows\SysWOW64\crypt32.dll`,
	`\Windows\SysWOW64\ws2_32.dll`,
	`\Windows\SysWOW64\msvcp_win.dll`,
	`\Windows\SysWOW64\win32u.dll`,
	`\Windows\SysWOW64\imm32.dll`,
	`\Windows\SysWOW64\setupapi.dll`,
	`\Windows\SysWOW64\cfgmgr32.dll`,
	`\Windows\SysWOW64\version.dll`,
	`\Windows\SysWOW64\shlwapi.dll`,
	`\Windows\SysWOW64\cabinet.dll`,
	`\Windows\SysWOW64\msi.dll`,
	`\Windows\SysWOW64\psapi.dll`,
	`\Windows\SysWOW64\powrprof.dll`,
	`\Windows\SysWOW64\sspicli.dll`,
	`\Windows\SysWOW64\profapi.dll`,
	`\Windows\SysWOW64\shcore.dll`,
	`\Windows\SysWOW64\imagehlp.dll`,
	`\Windows\SysWOW64\wintrust.dll`,
}

// WoW64TreePaths are directory trees extracted wholesale. The
// SysWOW64\downlevel directory has API-set DLLs that the loader resolves
// via API-set maps.
var WoW64TreePaths = []string{
	`\Windows\SysWOW64\downlevel`,
}

// WoW64WinSxSKeywords are matched against WinSxS directory names to
// discover WoW64 and x86/x64 emulation component packages on ARM64.
var WoW64WinSxSKeywords = []string{
	"microsoft-windows-wow64",
	"microsoft-windows-xtajit",
	"microsoft-windows-xta-",
}

// WoW64ServicingPackageKeywords filter servicing metadata from install.wim.
var WoW64ServicingPackageKeywords = []string{
	"microsoft-windows-wow64",
}
