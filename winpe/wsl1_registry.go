package winpe

import (
	"encoding/binary"
	"encoding/hex"
	"unicode/utf16"

	"github.com/devcell-sh/go-regedit"
)

const softwareHivePath = `\Windows\System32\config\SOFTWARE`

const (
	wslInstallDir = `X:\Program Files\WSL\`
	wslProductID  = `{D16124C0-602B-4D6D-945F-2D34B9FB4957}`
	wslVersion    = WSL1PackageVersion + `.0`
)

// encodeUTF16LE encodes s as NUL-terminated UTF-16LE bytes, suitable for
// REG_SZ and REG_EXPAND_SZ registry values.
func encodeUTF16LE(s string) []byte {
	units := utf16.Encode([]rune(s + "\x00"))
	out := make([]byte, len(units)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[i*2:], u)
	}
	return out
}

// encodeDWordLE encodes v as a 4-byte little-endian DWORD.
func encodeDWordLE(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func dwordValue(v uint32) regedit.Value {
	return regedit.Value{Type: regedit.TypeDWord, Data: encodeDWordLE(v)}
}

func szValue(s string) regedit.Value {
	return regedit.Value{Type: regedit.TypeString, Data: encodeUTF16LE(s)}
}

func expandSzValue(s string) regedit.Value {
	return regedit.Value{Type: regedit.TypeExpandString, Data: encodeUTF16LE(s)}
}

func multiSzValue(ss []string) regedit.Value {
	return regedit.Value{Type: regedit.TypeMultiString, Data: EncodeMultiSz(ss)}
}

func binaryValue(data []byte) regedit.Value {
	return regedit.Value{Type: regedit.TypeBinary, Data: data}
}

// WSL1ServicePatchSet registers the inbox WSL1 drivers and the packaged
// WSLService. The service and COM values mirror the official WSL ARM64 MSI;
// the only path adaptation is installing into WinPE's X: ramdisk.
//
// The patch targets boot.wim image 2. Callers apply it via PatchWIM after
// TransferWSL1Files has injected the binaries.
func WSL1ServicePatchSet() WimPatchSet {
	return WimPatchSet{
		ImageNum: 2,
		KeyWrites: []RegistryKeyWrite{
			wsl1BFSKeyWrite(),
			wsl1BindFltKeyWrite(),
			wsl1AFUnixKeyWrite(),
			wsl1WCIFSKeyWrite(),
			wsl1P9RdrKeyWrite(),
			wsl1P9NPKeyWrite(),
			wsl1NetworkProviderOrderKeyWrite(),
			wsl1LxssKeyWrite(),
			wslServiceKeyWrite(),
			wslSupportInterfaceKeyWrite(),
			wslSupportProxyKeyWrite(),
			wslInterfaceKeyWrite(),
			wslProxyKeyWrite(),
			wslAppIDKeyWrite(),
			wslSessionKeyWrite(),
			wslNotificationKeyWrite(),
			wslMSIKeyWrite(),
		},
	}
}

func wsl1BFSKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\bfs`,
		Spec: &regedit.Key{
			Name: "bfs",
			Values: map[string]regedit.Value{
				"DependOnService": multiSzValue([]string{"FltMgr"}),
				"Description":     szValue(`@%systemroot%\system32\drivers\bfs.sys,-101`),
				"DisplayName":     szValue(`@%systemroot%\system32\drivers\bfs.sys,-100`),
				"ErrorControl":    dwordValue(1),
				"Group":           szValue("FSFilter Virtualization"),
				"ImagePath":       expandSzValue(`\SystemRoot\system32\drivers\bfs.sys`),
				// Full Windows starts bfs automatically. WinPE must wait until the
				// transplanted catalog is registered in the live catalog database.
				"Start":             dwordValue(3),
				"SupportedFeatures": dwordValue(7),
				"Type":              dwordValue(2),
			},
			Subkeys: map[string]*regedit.Key{
				"Instances": {
					Values: map[string]regedit.Value{"DefaultInstance": szValue("bfs")},
					Subkeys: map[string]*regedit.Key{
						"bfs": {Values: map[string]regedit.Value{
							"Altitude": szValue("150000"),
							"Flags":    dwordValue(0),
						}},
					},
				},
				"Parameters": {Values: map[string]regedit.Value{
					"ProgramData": szValue(`X:\ProgramData`),
				}},
			},
		},
	}
}

func wsl1BindFltKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\bindflt`,
		Spec: &regedit.Key{
			Name: "bindflt",
			Values: map[string]regedit.Value{
				"DependOnService":   multiSzValue([]string{"FltMgr"}),
				"Description":       szValue(`@%systemroot%\system32\drivers\bindflt.sys,-101`),
				"DisplayName":       szValue(`@%systemroot%\system32\drivers\bindflt.sys,-100`),
				"ErrorControl":      dwordValue(1),
				"Group":             szValue("FSFilter Top"),
				"ImagePath":         expandSzValue(`\SystemRoot\system32\drivers\bindflt.sys`),
				"Start":             dwordValue(3),
				"SupportedFeatures": dwordValue(0xf),
				"Type":              dwordValue(2),
			},
			Subkeys: map[string]*regedit.Key{
				"Instances": {
					Values: map[string]regedit.Value{"DefaultInstance": szValue("bindflt Instance")},
					Subkeys: map[string]*regedit.Key{
						"bindflt Instance": {Values: map[string]regedit.Value{
							"Altitude": szValue("409800"),
							"Flags":    dwordValue(0),
						}},
					},
				},
				"Parameters": {Values: map[string]regedit.Value{"DebugOptions": dwordValue(0)}},
				"SharedState": {Values: map[string]regedit.Value{
					"WppRecorder_TraceGuid": szValue(`{1fd216eb-201e-4b4d-93ca-41f33d5a04ec}`),
				}},
			},
		},
	}
}

func wsl1AFUnixKeyWrite() RegistryKeyWrite {
	winsockProtocol := &regedit.Key{Values: map[string]regedit.Value{
		"Version":           dwordValue(2),
		"AddressFamily":     dwordValue(1),
		"MaxSockAddrLength": dwordValue(0x6e),
		"MinSockAddrLength": dwordValue(0x2),
		"SocketType":        dwordValue(1),
		"Protocol":          dwordValue(0),
		"ProtocolMaxOffset": dwordValue(0),
		"ByteOrder":         dwordValue(0),
		"MessageSize":       dwordValue(0),
		"szProtocol":        expandSzValue("AF_UNIX"),
		"ProviderFlags":     dwordValue(0x8),
		"ServiceFlags":      dwordValue(0x20026),
	}}
	winsock := &regedit.Key{
		Values: map[string]regedit.Value{
			"MaxSockAddrLength": dwordValue(0x6e),
			"MinSockAddrLength": dwordValue(0x2),
			"HelperDllName":     expandSzValue(`%SystemRoot%\system32\wshunix.dll`),
			"ProviderGUID":      binaryValue(mustDecodeHex("d94309a02e9c33469b590057a3160994")),
			"OfflineCapable":    dwordValue(1),
			"Mapping":           binaryValue(mustDecodeHex("0100000003000000010000000100000000000000")),
		},
		Subkeys: map[string]*regedit.Key{"0": winsockProtocol},
	}
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\afunix`,
		Spec: &regedit.Key{
			Name: "afunix",
			Values: map[string]regedit.Value{
				"DisplayName":  szValue("afunix"),
				"ErrorControl": dwordValue(1),
				"Group":        szValue("PNP_TDI"),
				"ImagePath":    expandSzValue(`\SystemRoot\system32\drivers\afunix.sys`),
				// WinPE registers transplanted component catalogs after boot. Keep
				// this driver demand-start so Code Integrity does not reject and
				// cache it before the matching catalog is available.
				"Start": dwordValue(3),
				"Type":  dwordValue(1),
			},
			Subkeys: map[string]*regedit.Key{
				"Parameters": {Subkeys: map[string]*regedit.Key{"Winsock": winsock}},
			},
		},
	}
}

func wsl1WCIFSKeyWrite() RegistryKeyWrite {
	instances := &regedit.Key{
		Values: map[string]regedit.Value{
			"DefaultInstance": szValue("wcifs Instance"),
			"OuterInstance":   szValue("wcifs Outer Instance"),
		},
		Subkeys: map[string]*regedit.Key{
			"wcifs Instance": {Values: map[string]regedit.Value{
				"Altitude": szValue("189900"),
				"Flags":    dwordValue(0),
			}},
			"wcifs Outer Instance": {Values: map[string]regedit.Value{
				"Altitude": szValue("189899"),
				"Flags":    dwordValue(0),
			}},
		},
	}
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\wcifs`,
		Spec: &regedit.Key{
			Name: "wcifs",
			Values: map[string]regedit.Value{
				"DependOnService": multiSzValue([]string{"FltMgr"}),
				"Description":     szValue(`@%systemroot%\system32\drivers\wcifs.sys,-101`),
				"DisplayName":     szValue(`@%systemroot%\system32\drivers\wcifs.sys,-100`),
				"ErrorControl":    dwordValue(1),
				"Group":           szValue("FSFilter Virtualization"),
				"ImagePath":       expandSzValue(`\SystemRoot\system32\drivers\wcifs.sys`),
				// See afunix above: load only after live catalog registration.
				"Start": dwordValue(3),
				"Type":  dwordValue(2),
			},
			Subkeys: map[string]*regedit.Key{
				"Parameters": {
					Values: map[string]regedit.Value{
						"DebugOptions":      dwordValue(0xc),
						"SupportedFeatures": dwordValue(0xf),
					},
					Subkeys: map[string]*regedit.Key{"Instances": instances},
				},
				"SharedState": {Values: map[string]regedit.Value{
					"WppRecorder_TraceGuid": szValue(`{803cb23a-e32b-4200-bd82-d8a15919ac1b}`),
				}},
			},
		},
	}
}

func wsl1P9RdrKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\P9Rdr`,
		Spec: &regedit.Key{
			Name: "P9Rdr",
			Values: map[string]regedit.Value{
				"DependOnService": multiSzValue([]string{"RDBSS"}),
				"Description":     szValue(`@%SystemRoot%\System32\drivers\p9rdr.sys,-101`),
				"DisplayName":     szValue(`@%SystemRoot%\System32\drivers\p9rdr.sys,-100`),
				"ErrorControl":    dwordValue(1),
				"ImagePath":       expandSzValue(`System32\drivers\p9rdr.sys`),
				// Register its transplanted catalog before demand-starting it.
				"Start": dwordValue(3),
				"Type":  dwordValue(1),
			},
		},
	}
}

func wsl1P9NPKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\P9NP`,
		Spec: &regedit.Key{
			Name: "P9NP",
			Values: map[string]regedit.Value{
				"Description": expandSzValue(`@%systemroot%\system32\p9np.dll,-101`),
				"DisplayName": expandSzValue(`@%systemroot%\system32\p9np.dll,-100`),
			},
			Subkeys: map[string]*regedit.Key{
				"NetworkProvider": {Values: map[string]regedit.Value{
					"DeviceName":                       szValue(`\Device\P9Rdr`),
					"DisplayName":                      expandSzValue(`@%systemroot%\system32\p9np.dll,-100`),
					"Name":                             szValue("Plan 9 Network Provider"),
					"ProviderPath":                     expandSzValue(`%SystemRoot%\System32\p9np.dll`),
					"TriggerStartCompleteNotification": binaryValue(mustDecodeHex("7510bca3541ec641")),
					"TriggerStartNotification":         binaryValue(mustDecodeHex("7508bca3541ec641")),
					"TriggerStartPrefix":               multiSzValue([]string{"wsl.localhost", "wsl$"}),
				}},
			},
		},
	}
}

func wsl1NetworkProviderOrderKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Control\NetworkProvider\Order`,
		Spec: &regedit.Key{Values: map[string]regedit.Value{
			"ProviderOrder": szValue("P9NP,LanmanWorkstation"),
		}},
	}
}

func wsl1LxssKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\lxss`,
		Spec: &regedit.Key{
			Name: "lxss",
			Values: map[string]regedit.Value{
				"Description":  szValue(`@%SystemRoot%\system32\drivers\lxss.sys,-101`),
				"DisplayName":  szValue(`@%SystemRoot%\system32\drivers\lxss.sys,-100`),
				"ErrorControl": dwordValue(1),
				"ImagePath":    expandSzValue(`system32\drivers\lxss.sys`),
				"Start":        dwordValue(0), // SERVICE_BOOT_START: Pico registration is boot-only
				"Type":         dwordValue(1), // SERVICE_KERNEL_DRIVER
			},
		},
	}
}

func wslSupportInterfaceKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: softwareHivePath,
		KeyPath:  `Classes\Interface\{46f3c96d-ffa3-42f0-b052-52f5e7ecbb08}`,
		Spec: &regedit.Key{
			Values: map[string]regedit.Value{"": szValue("IWslSupport")},
			Subkeys: map[string]*regedit.Key{
				"ProxyStubClsid32": {Values: map[string]regedit.Value{
					"": szValue(`{e66b0f30-e7b4-4f8c-acfd-d100c46c6278}`),
				}},
			},
		},
	}
}

func wslSupportProxyKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: softwareHivePath,
		KeyPath:  `Classes\CLSID\{e66b0f30-e7b4-4f8c-acfd-d100c46c6278}`,
		Spec: &regedit.Key{
			Values: map[string]regedit.Value{"": szValue("PSFactoryBuffer")},
			Subkeys: map[string]*regedit.Key{
				"InProcServer32": {Values: map[string]regedit.Value{
					"":               szValue(`X:\Windows\System32\lxss\wslsupport.dll`),
					"ThreadingModel": szValue("Both"),
				}},
			},
		},
	}
}

func wslServiceKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\WSLService`,
		Spec: &regedit.Key{
			Name: "WSLService",
			Values: map[string]regedit.Value{
				"Type":         dwordValue(16), // SERVICE_WIN32_OWN_PROCESS
				"Start":        dwordValue(3),  // SERVICE_DEMAND_START after catalog registration
				"ErrorControl": dwordValue(1),
				"ImagePath":    expandSzValue(`"%SystemDrive%\Program Files\WSL\wslservice.exe"`),
				"ObjectName":   szValue("LocalSystem"),
				"DisplayName":  szValue("WSL Service"),
			},
		},
	}
}

func wslInterfaceKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: softwareHivePath,
		KeyPath:  `Classes\Interface\{38541BDC-F54F-4CEB-85D0-37F0F3D2617E}`,
		Spec: &regedit.Key{
			Values: map[string]regedit.Value{"": szValue("ILxssUserSession")},
			Subkeys: map[string]*regedit.Key{
				"ProxyStubClsid32": {
					Values: map[string]regedit.Value{
						"": szValue(`{4EA0C6DD-E9FF-48E7-994E-13A31D10DC60}`),
					},
				},
			},
		},
	}
}

func wslProxyKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: softwareHivePath,
		KeyPath:  `Classes\CLSID\{4EA0C6DD-E9FF-48E7-994E-13A31D10DC60}`,
		Spec: &regedit.Key{
			Values: map[string]regedit.Value{"": szValue("PSFactoryBuffer")},
			Subkeys: map[string]*regedit.Key{
				"InProcServer32": {
					Values: map[string]regedit.Value{
						"":               szValue(wslInstallDir + "wslserviceproxystub.dll"),
						"ThreadingModel": szValue("Both"),
					},
				},
			},
		},
	}
}

func wslAppIDKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: softwareHivePath,
		KeyPath:  `Classes\AppID\{370121D2-AA7E-4608-A86D-0BBAB9DA1A60}`,
		Spec: &regedit.Key{Values: map[string]regedit.Value{
			"AccessPermission": binaryValue(wslDCOMPermission()),
			"LaunchPermission": binaryValue(wslDCOMPermission()),
			"LocalService":     szValue("WSLService"),
		}},
	}
}

func wslSessionKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: softwareHivePath,
		KeyPath:  `Classes\CLSID\{a9b7a1b9-0671-405c-95f1-e0612cb4ce7e}`,
		Spec: &regedit.Key{Values: map[string]regedit.Value{
			"":      szValue("LxssUserSession"),
			"AppId": szValue(`{370121D2-AA7E-4608-A86D-0BBAB9DA1A60}`),
		}},
	}
}

func wslNotificationKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: softwareHivePath,
		KeyPath:  `Classes\CLSID\{2B9C59C3-98F1-45C8-B87B-12AE3C7927E8}`,
		Spec: &regedit.Key{Subkeys: map[string]*regedit.Key{
			"LocalServer32": {
				Values: map[string]regedit.Value{
					"": szValue(`"` + wslInstallDir + `wslhost.exe"`),
				},
			},
		}},
	}
}

func wslMSIKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: softwareHivePath,
		KeyPath:  `Microsoft\Windows\CurrentVersion\Lxss`,
		Spec: &regedit.Key{Subkeys: map[string]*regedit.Key{
			"MSI": {Values: map[string]regedit.Value{
				"InstallLocation": szValue(wslInstallDir),
				"ProductCode":     szValue(wslProductID),
				"Version":         szValue(wslVersion),
			}},
		}},
	}
}

func wslDCOMPermission() []byte {
	const encoded = "01000480580000006800000000000000140000000200440003000000000014000b00000001010000000000050b000000000014000b00000001010000000000050a000000000014000b0000000101000000000005120000000102000000000005200000002002000001020000000000052000000020020000"
	return mustDecodeHex(encoded)
}

func mustDecodeHex(encoded string) []byte {
	data, err := hex.DecodeString(encoded)
	if err != nil {
		panic(err)
	}
	return data
}
