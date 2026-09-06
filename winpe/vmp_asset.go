package winpe

import _ "embed"

// vmpServicesReg is the VMP service-key registry export cloned into boot.wim's
// SYSTEM hive during the transplant. Ground truth: a live ARM64 Windows 11 VM
// with only VirtualMachinePlatform enabled (see assets/vmp-services.reg and
// testdata/live-vm-vmp-services.txt). Embedded so the CLI can build the hpv
// stack without any test-scoped file.
//
//go:embed assets/vmp-services.reg
var vmpServicesReg []byte

// VMPServicesRegExport returns the embedded VMP service registry export.
func VMPServicesRegExport() []byte {
	return vmpServicesReg
}
