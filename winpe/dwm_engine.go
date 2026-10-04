package winpe

// DWMSystem32Paths are the user-mode DLLs and executables that make up
// the DWM compositor and its DirectX rendering pipeline. Stock WinPE
// carries only the dwmapi.dll stub; everything else must be transplanted
// from install.wim to enable DirectComposition (required by WebView2 and
// other Chromium-based renderers).
var DWMSystem32Paths = []string{
	// DWM compositor core
	`\Windows\System32\dwm.exe`,
	`\Windows\System32\dwmcore.dll`,
	`\Windows\System32\dwminit.dll`,
	`\Windows\System32\dwmscene.dll`,
	`\Windows\System32\dwmredir.dll`,
	`\Windows\System32\dwmghost.dll`,
	`\Windows\System32\uDWM.dll`,

	// DirectComposition: the API surface that WebView2 renders into
	`\Windows\System32\dcomp.dll`,

	// WARP software rasterizer (no GPU driver needed)
	`\Windows\System32\Microsoft.Internal.WarpPal.dll`,

	// DirectX: DXGI + D3D11 + WARP + D3D12
	`\Windows\System32\d3d11.dll`,
	`\Windows\System32\dxgi.dll`,
	`\Windows\System32\d3d10warp.dll`,
	`\Windows\System32\D3D12.dll`,
	`\Windows\System32\D3D12Core.dll`,
	`\Windows\System32\DXCore.dll`,
	`\Windows\System32\d3d11on12.dll`,
	`\Windows\System32\dxilconv.dll`,

	// dxgkrnl adapter database: without this, dxgkrnl cannot classify
	// adapters during post-start and rejects them with error 43
	// (CM_PROB_FAILED_POST_START).
	`\Windows\System32\DirectXDatabase.dll`,
	`\Windows\System32\DirectXDatabaseHelper.dll`,
	`\Windows\System32\DXCoreSharedLib.dll`,
	`\Windows\System32\dxgiadaptercache.exe`,

	// CDD (Canonical Display Driver): the GDI-to-DX bridge that DWM
	// composites through. Present in boot.wim but listed here so the
	// transplant from install.wim upgrades it if the versions differ.
	`\Windows\System32\cdd.dll`,

	// 2D/text rendering (Chromium depends on Direct2D + DirectWrite)
	`\Windows\System32\d2d1.dll`,
	`\Windows\System32\DWrite.dll`,
	`\Windows\System32\D3DCompiler_47.dll`,

	// Shader cache and diagnostics
	`\Windows\System32\D3DSCache.dll`,
	`\Windows\System32\dxdiagn.dll`,
	`\Windows\System32\DXGIDebug.dll`,

	// Media Foundation (WebView2 needs these for early init)
	`\Windows\System32\mf.dll`,
	`\Windows\System32\mfplat.dll`,

	// Display/GPU management
	`\Windows\System32\GraphicsPerformanceMonitor.dll`,
	`\Windows\System32\DispBroker.Desktop.dll`,
	`\Windows\System32\DispBroker.dll`,

	// DXGI adapter enumeration runtime
	`\Windows\System32\dxgitrace.dll`,
}

// DWMWinSxSKeywords are matched against WinSxS directory names to
// discover DWM component packages. The naming follows the CBS pattern
// used by the WSL1 transplant.
var DWMWinSxSKeywords = []string{
	"microsoft-windows-d..opwindowmanager",
	"microsoft-windows-d..ager-api",
	"microsoft-windows-d..ager-udwm",
	"microsoft-windows-directx-d3d11",
	"microsoft-windows-directx-dxgi",
	"microsoft-windows-directx-warp",
	"microsoft-windows-directcomposition",
	"microsoft-windows-warppal",
	"microsoft-windows-direct2d",
	"microsoft-windows-directwrite",
	"microsoft-windows-display-msdv",
	"microsoft-windows-directx-dxcore",
	"microsoft-windows-monitor-inf",
	"microsoft-windows-directxdatabase",
	"microsoft-windows-directx-d3d12",
	"microsoft-windows-directx-direct3d-runtime",
	"microsoft-windows-cdd",
	"microsoft-windows-graphicsperfmonitor",
	"microsoft-windows-dispbroker",
}

// DWMServicingPackageKeywords filter servicing metadata entries from
// install.wim for inclusion in boot.wim. These match the Win3-DWM
// and ClientCore-DWM package names.
var DWMServicingPackageKeywords = []string{
	"win3-dwm",
	"windows-clientcore-dwm",
}

// DWMDriverPaths are kernel-mode driver binaries needed by the DWM
// display pipeline. monitor.sys is the function driver for child
// monitor devices created by WDDM adapters (BasicDisplay,
// VioGpuDod); without it VidPn targets cannot be fully mapped.
var DWMDriverPaths = []string{
	`\Windows\System32\drivers\monitor.sys`,
}

// DWMDriverStoreKeywords are matched against DriverStore FileRepository
// directory names to discover display driver packages that must be
// present in boot.wim for PnP to install display adapters and monitors.
// msdv.inf is not at \Windows\INF\ on ARM64; it lives only in the
// DriverStore FileRepository.
var DWMDriverStoreKeywords = []string{
	"msdv.inf",
	"basicrender.inf",
	"basicdisplay.inf",
	"monitor.inf",
}

// DWMINFPaths are display-related INF files from install.wim that
// viogpudo and other WDDM display drivers depend on. viogpudo.inf
// has Include=msdv.inf; without it the driver may fail to initialize.
// monitor.inf classifies child monitor devices (DISPLAY\Default_Monitor)
// under the Monitor class so they receive the monitor.sys function driver.
var DWMINFPaths = []string{
	`\Windows\INF\msdv.inf`,
	`\Windows\INF\c_display.inf`,
	`\Windows\INF\monitor.inf`,
}
