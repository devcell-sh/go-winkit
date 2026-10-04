//go:build !cgo

package winpe

func TransferSerialDriver(installWimPath, bootWimPath string) error {
	return errNoWimlib
}
