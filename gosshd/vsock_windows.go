//go:build windows

package gosshd

import (
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	afVsock      = 40         // AF_VSOCK, provided by viosock.sys (virtio-win)
	vmaddrCIDAny = 0xFFFFFFFF // VMADDR_CID_ANY: accept from any CID
)

type sockaddrVM struct {
	Family    uint16
	Reserved1 uint16
	Port      uint32
	CID       uint32
	Zero      [4]byte
}

var (
	modWs232   = windows.NewLazySystemDLL("ws2_32.dll")
	procBind   = modWs232.NewProc("bind")
	procListen = modWs232.NewProc("listen")
	procAccept = modWs232.NewProc("accept")
	procRecv   = modWs232.NewProc("recv")
	procSend   = modWs232.NewProc("send")
	procClose  = modWs232.NewProc("closesocket")
)

// ListenVsock creates a net.Listener on the given virtio-vsock port. Requires
// the viosock.sys driver from virtio-win to be loaded in the guest.
func ListenVsock(port uint32) (net.Listener, error) {
	fd, err := windows.Socket(afVsock, windows.SOCK_STREAM, 0)
	if err != nil {
		return nil, &net.OpError{Op: "socket", Net: "vsock", Err: err}
	}

	addr := sockaddrVM{
		Family: uint16(afVsock),
		Port:   port,
		CID:    vmaddrCIDAny,
	}
	r, _, e := syscall.SyscallN(procBind.Addr(),
		uintptr(fd),
		uintptr(unsafe.Pointer(&addr)),
		uintptr(unsafe.Sizeof(addr)),
	)
	if r != 0 {
		windows.CloseHandle(fd)
		return nil, &net.OpError{Op: "bind", Net: "vsock", Err: e}
	}

	r, _, e = syscall.SyscallN(procListen.Addr(), uintptr(fd), 5)
	if r != 0 {
		windows.CloseHandle(fd)
		return nil, &net.OpError{Op: "listen", Net: "vsock", Err: e}
	}

	return &vsockListener{fd: fd, port: port}, nil
}

type vsockListener struct {
	fd   windows.Handle
	port uint32
	once sync.Once
}

func (l *vsockListener) Accept() (net.Conn, error) {
	newfd, _, e := syscall.SyscallN(procAccept.Addr(), uintptr(l.fd), 0, 0)
	h := windows.Handle(newfd)
	if h == windows.InvalidHandle {
		return nil, &net.OpError{Op: "accept", Net: "vsock", Err: e}
	}
	return &vsockConn{fd: h}, nil
}

func (l *vsockListener) Close() error {
	var err error
	l.once.Do(func() {
		r, _, e := syscall.SyscallN(procClose.Addr(), uintptr(l.fd))
		if r != 0 {
			err = e
		}
	})
	return err
}

func (l *vsockListener) Addr() net.Addr {
	return VsockAddr(l.port)
}

// VsockAddr is a vsock port address.
type VsockAddr uint32

func (a VsockAddr) Network() string { return "vsock" }
func (a VsockAddr) String() string  { return fmt.Sprintf("vsock://:%d", uint32(a)) }

type vsockConn struct {
	fd   windows.Handle
	once sync.Once
}

func (c *vsockConn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n, _, e := syscall.SyscallN(procRecv.Addr(),
		uintptr(c.fd),
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
		0,
	)
	if n == 0 && len(b) > 0 {
		return 0, io.EOF
	}
	if int(n) < 0 {
		return 0, &net.OpError{Op: "read", Net: "vsock", Err: e}
	}
	return int(n), nil
}

func (c *vsockConn) Write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	n, _, e := syscall.SyscallN(procSend.Addr(),
		uintptr(c.fd),
		uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)),
		0,
	)
	if int(n) < 0 {
		return 0, &net.OpError{Op: "write", Net: "vsock", Err: e}
	}
	return int(n), nil
}

func (c *vsockConn) Close() error {
	var err error
	c.once.Do(func() {
		r, _, e := syscall.SyscallN(procClose.Addr(), uintptr(c.fd))
		if r != 0 {
			err = e
		}
	})
	return err
}

func (c *vsockConn) LocalAddr() net.Addr              { return VsockAddr(0) }
func (c *vsockConn) RemoteAddr() net.Addr             { return VsockAddr(0) }
func (c *vsockConn) SetDeadline(time.Time) error      { return nil }
func (c *vsockConn) SetReadDeadline(time.Time) error  { return nil }
func (c *vsockConn) SetWriteDeadline(time.Time) error { return nil }
