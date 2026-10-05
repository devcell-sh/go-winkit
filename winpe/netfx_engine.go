package winpe

// NetFxSystem32Paths are the .NET Framework files needed to run managed
// assemblies (like choco.exe) in WinPE. Stock WinPE carries only the
// WinPE-NetFx optional component's subset; a full install has the
// complete v4.0.30319 tree. The CLR hosting shim (mscoree.dll) and the
// runtime tree are extracted from install.wim and injected into boot.wim.
var NetFxSystem32Paths = []string{
	`\Windows\System32\mscoree.dll`,
	`\Windows\System32\mscoreei.dll`,
	`\Windows\System32\clrjit.dll`,
	`\Windows\System32\clr.dll`,
	`\Windows\System32\msvcr120_clr0400.dll`,
}

// NetFxTreePaths are directory trees extracted wholesale from install.wim.
// The v4.0.30319 directory contains the CLR, BCL assemblies, and GAC
// entries that managed applications link against.
var NetFxTreePaths = []string{
	`\Windows\Microsoft.NET`,
}

// NetFxWinSxSKeywords are matched against WinSxS directory names to
// discover .NET Framework component packages. Any WinSxS entry whose
// lowercase name contains one of these is extracted.
var NetFxWinSxSKeywords = []string{
	"microsoft-windows-netfx",
	"microsoft-windows-m..core-runtime",
	"microsoft-windows-m..l-runtime",
	"microsoft-windows-mscoree",
}

// NetFxServicingPackageKeywords filter servicing metadata from install.wim.
var NetFxServicingPackageKeywords = []string{
	"microsoft-windows-netfx",
}
