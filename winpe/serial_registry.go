package winpe

import "github.com/devcell-sh/go-regedit"

// SerialDriverPatchSet returns a WimPatchSet that registers the Serial
// and Serenum kernel drivers in the SYSTEM hive so PnP can start them
// when it detects matching PCI devices during wpeinit.
func SerialDriverPatchSet() WimPatchSet {
	return WimPatchSet{
		ImageNum: 2,
		KeyWrites: []RegistryKeyWrite{
			serialDriverKeyWrite(),
			serenumDriverKeyWrite(),
		},
	}
}

func serialDriverKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\Serial`,
		Spec: &regedit.Key{
			Name: "Serial",
			Values: map[string]regedit.Value{
				"ErrorControl": dwordValue(1),
				"Group":        szValue("Extended base"),
				"ImagePath":    expandSzValue(`system32\drivers\serial.sys`),
				"Start":        dwordValue(3),
				"Type":         dwordValue(1),
			},
		},
	}
}

func serenumDriverKeyWrite() RegistryKeyWrite {
	return RegistryKeyWrite{
		HivePath: systemHivePath,
		KeyPath:  `ControlSet001\Services\Serenum`,
		Spec: &regedit.Key{
			Name: "Serenum",
			Values: map[string]regedit.Value{
				"ErrorControl": dwordValue(1),
				"Group":        szValue("PnP Filter"),
				"ImagePath":    expandSzValue(`system32\drivers\serenum.sys`),
				"Start":        dwordValue(3),
				"Type":         dwordValue(1),
			},
		},
	}
}
