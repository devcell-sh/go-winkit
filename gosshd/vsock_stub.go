//go:build !windows

package gosshd

import (
	"fmt"
	"net"
)

// VsockAddr is a vsock port address.
type VsockAddr uint32

func (a VsockAddr) Network() string { return "vsock" }
func (a VsockAddr) String() string  { return fmt.Sprintf("vsock://:%d", uint32(a)) }

// ListenVsock returns an error on non-Windows platforms. The viosock.sys
// driver that provides AF_VSOCK is Windows-only (virtio-win).
func ListenVsock(port uint32) (net.Listener, error) {
	return nil, fmt.Errorf("vsock listener requires Windows with viosock.sys driver")
}
