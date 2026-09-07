//go:build darwin

package vz

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"github.com/tmc/apple/virtualization"
)

// startVsockProxy starts a TCP listener on the given host port and proxies
// each connection to the guest's vsock port via VirtioSocketDevice.ConnectToPort().
// The proxy runs in the background and is stopped when the vmHandle is stopped.
func (h *vmHandle) startVsockProxy(hostPort uint16, guestVsockPort uint32) error {
	devices := h.vm.SocketDevices()
	if len(devices) == 0 {
		return fmt.Errorf("no vsock device found on VM")
	}
	vsockDev := virtualization.VZVirtioSocketDeviceFromID(devices[0].ID)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", hostPort))
	if err != nil {
		return fmt.Errorf("listening on host port %d: %w", hostPort, err)
	}

	h.proxyListener = ln
	h.proxyWg = &sync.WaitGroup{}

	h.proxyWg.Add(1)
	go func() {
		defer h.proxyWg.Done()
		var connCount int
		for {
			tcpConn, err := ln.Accept()
			if err != nil {
				return
			}
			connCount++
			h.proxyWg.Add(1)
			go func(n int) {
				defer h.proxyWg.Done()
				h.proxyOneConn(tcpConn, vsockDev, guestVsockPort, n)
			}(connCount)
		}
	}()
	return nil
}

func (h *vmHandle) connectToPortOnQueue(ctx context.Context, dev virtualization.VZVirtioSocketDevice, port uint32) (*virtualization.VZVirtioSocketConnection, error) {
	type connResult struct {
		conn *virtualization.VZVirtioSocketConnection
		err  error
	}
	r, ctxErr := dispatchCall(h.queue, ctx, func(done func(connResult)) {
		dev.ConnectToPortCompletionHandler(port, func(conn *virtualization.VZVirtioSocketConnection, err error) {
			done(connResult{conn, err})
		})
	})
	if ctxErr != nil {
		return nil, ctxErr
	}
	return r.conn, r.err
}

func (h *vmHandle) proxyOneConn(tcp net.Conn, dev virtualization.VZVirtioSocketDevice, port uint32, connNum int) {
	defer tcp.Close()
	vsockConn, err := h.connectToPortOnQueue(context.Background(), dev, port)
	if err != nil {
		if connNum <= 2 {
			h.logger.Info("vz: vsock Connect failed (expected during boot)", "port", port, "conn", connNum, "err", err)
		}
		return
	}

	vsock := os.NewFile(uintptr(vsockConn.FileDescriptor()), "vsock")
	defer func() {
		vsock.Close()
		vsockConn.Close()
	}()
	h.logger.Info("vz: vsock connection established", "guestPort", port, "conn", connNum)

	done := make(chan struct{})
	go func() {
		io.Copy(tcp, vsock)
		close(done)
	}()
	io.Copy(vsock, tcp)
	<-done
}
