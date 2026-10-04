package winpe

import (
	"github.com/devcell-sh/go-regedit"
)

// DWMServicePatchSet registers the Desktop Window Manager service and
// its DirectX dependencies in the boot.wim SYSTEM hive. DWM runs as a
// SYSTEM service; on full Windows it is auto-started by the session
// manager. In WinPE we register it for demand-start so winkit-service
// can start it at the right point in the boot sequence (after display
// drivers are loaded).
//
// The patch set also writes SOFTWARE hive keys that configure DWM for
// software composition (WARP). Without these, DWM may refuse to enable
// composition when no hardware WDDM adapter reports display sources.
func DWMServicePatchSet() WimPatchSet {
	return WimPatchSet{
		ImageNum: 2,
		KeyWrites: []RegistryKeyWrite{
			dwmServiceKeyWrite(),
			dwmCoreDriverKeyWrite(),
			dxgmms1DriverKeyWrite(),
			dxgmms2DriverKeyWrite(),
			dwmSoftwareCompositionKeyWrite(),
			displayClassKeyWrite(),
			monitorClassKeyWrite(),
		},
	}
}

// dwmServiceKeyWrite registers dwm.exe as the Desktop Window Manager
// service. The service type is WIN32_OWN_PROCESS (0x10). Start=3
// (demand-start) so winkit-service can control when DWM launches.
func dwmServiceKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\DWM`,
		Spec: &regedit.Key{
			Name: "DWM",
			Values: map[string]regedit.Value{
				"Type":         dwordValue(0x10), // SERVICE_WIN32_OWN_PROCESS
				"Start":        dwordValue(3),    // SERVICE_DEMAND_START
				"ErrorControl": dwordValue(1),    // SERVICE_ERROR_NORMAL
				"ImagePath":    expandSzValue(`%SystemRoot%\system32\dwm.exe`),
				"DisplayName":  szValue("Desktop Window Manager"),
				"ObjectName":   szValue("LocalSystem"),
			},
		},
	}
}

// dwmCoreDriverKeyWrite registers the BasicRender adapter service
// entry. Full Windows creates this during setup; WinPE needs it
// pre-registered so dxgkrnl can load the WARP software adapter.
func dwmCoreDriverKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\BasicRender`,
		Spec: &regedit.Key{
			Name: "BasicRender",
			Values: map[string]regedit.Value{
				"Type":         dwordValue(1), // SERVICE_KERNEL_DRIVER
				"Start":        dwordValue(1), // SERVICE_SYSTEM_START
				"ErrorControl": dwordValue(0), // SERVICE_ERROR_IGNORE
				"Group":        szValue("Video"),
				"DisplayName":  szValue("Microsoft Basic Render Driver"),
			},
		},
	}
}

func dxgmms1DriverKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\dxgmms1`,
		Spec: &regedit.Key{
			Name: "dxgmms1",
			Values: map[string]regedit.Value{
				"Type":         dwordValue(1), // SERVICE_KERNEL_DRIVER
				"Start":        dwordValue(0), // SERVICE_BOOT_START
				"ErrorControl": dwordValue(0), // SERVICE_ERROR_IGNORE
				"Group":        szValue("Video"),
				"ImagePath":    szValue(`System32\drivers\dxgmms1.sys`),
				"DisplayName":  szValue("LDDM Graphics Memory Management 1"),
			},
		},
	}
}

func dxgmms2DriverKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\dxgmms2`,
		Spec: &regedit.Key{
			Name: "dxgmms2",
			Values: map[string]regedit.Value{
				"Type":         dwordValue(1), // SERVICE_KERNEL_DRIVER
				"Start":        dwordValue(0), // SERVICE_BOOT_START
				"ErrorControl": dwordValue(0), // SERVICE_ERROR_IGNORE
				"Group":        szValue("Video"),
				"ImagePath":    szValue(`System32\drivers\dxgmms2.sys`),
				"DisplayName":  szValue("LDDM Graphics Memory Management 2"),
			},
		},
	}
}

// displayClassKeyWrite registers the Display device class in the PnP
// class registry. WinPE ships without this entry, so PnP cannot
// classify GPU adapters (VioGpuDod, BasicDisplay) or install their
// class-specific co-installers. The entry must be present before PnP
// enumerates the PCI bus at boot.
func displayClassKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`,
		Spec: &regedit.Key{
			Name: "{4d36e968-e325-11ce-bfc1-08002be10318}",
			Values: map[string]regedit.Value{
				"Class": szValue("Display"),
			},
		},
	}
}

// monitorClassKeyWrite registers the Monitor device class. Without
// this, child monitor devices reported by WDDM adapters stay as
// "Unknown" class and never get a VidPn source-to-target mapping.
func monitorClassKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Control\Class\{4d36e96e-e325-11ce-bfc1-08002be10318}`,
		Spec: &regedit.Key{
			Name: "{4d36e96e-e325-11ce-bfc1-08002be10318}",
			Values: map[string]regedit.Value{
				"Class":          szValue("Monitor"),
				"NoInstallClass": szValue("1"),
			},
		},
	}
}

// dwmSoftwareCompositionKeyWrite configures the DWM policy keys in the
// SOFTWARE hive. These tell DWM to use software composition (WARP
// rasterizer) and not require hardware GPU acceleration. On full
// Windows these defaults come from the installed display driver; in
// WinPE we must set them explicitly.
func dwmSoftwareCompositionKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: softwareHivePath,
		KeyPath:  `Microsoft\Windows\DWM`,
		Spec: &regedit.Key{
			Name: "DWM",
			Values: map[string]regedit.Value{
				"Composition":     dwordValue(1), // enable composition
				"ForceEffectMode": dwordValue(0), // allow effects
				"UseMachineCheck": dwordValue(0), // skip hardware capability check
			},
		},
	}
}
