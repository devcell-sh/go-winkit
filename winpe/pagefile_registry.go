package winpe

import "github.com/devcell-sh/go-regedit"

// PagefilePatchSet returns a WimPatchSet that disables the WinPE
// auto-pagefile by clearing the PagingFiles registry value. Without
// this, Session Manager creates a 50 MB pagefile on the ramdisk and
// wpeutil CreatePageFile refuses to add a second one (0x80070061).
// With PagingFiles empty, the init/activate code can create a large
// pagefile on the persistent NTFS data disk via wpeutil.
func PagefilePatchSet() WimPatchSet {
	return WimPatchSet{
		ImageNum: 2,
		KeyWrites: []RegistryKeyWrite{
			{
				HivePath: systemHivePath,
				KeyPath:  `ControlSet001\Control\Session Manager\Memory Management`,
				Spec: &regedit.Key{
					Values: map[string]regedit.Value{
						"PagingFiles": multiSzValue(nil),
					},
				},
			},
		},
	}
}
