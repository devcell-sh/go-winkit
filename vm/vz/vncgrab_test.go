package vz

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVNCGrabFrame_FakeServer(t *testing.T) {
	const width, height = 4, 2

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- fakeRFBServer(ln, width, height)
	}()

	pngData, err := VNCGrabFrame(ln.Addr().String(), 5*time.Second)
	require.NoError(t, err)
	require.NotEmpty(t, pngData)

	img, err := png.Decode(bytes.NewReader(pngData))
	require.NoError(t, err)
	assert.Equal(t, width, img.Bounds().Dx())
	assert.Equal(t, height, img.Bounds().Dy())

	r, g, b, a := img.At(0, 0).RGBA()
	assert.Equal(t, uint32(0xFFFF), r, "red channel")
	assert.Equal(t, uint32(0), g, "green channel")
	assert.Equal(t, uint32(0), b, "blue channel")
	assert.Equal(t, uint32(0xFFFF), a, "alpha channel")

	require.NoError(t, <-serverDone)
}

func TestVNCGrabFrame_ConnectionRefused(t *testing.T) {
	_, err := VNCGrabFrame("127.0.0.1:1", 500*time.Millisecond)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "vnc dial")
}

func fakeRFBServer(ln net.Listener, width, height uint16) error {
	conn, err := ln.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()

	// ProtocolVersion
	conn.Write([]byte("RFB 003.008\n"))
	ver := make([]byte, 12)
	if _, err := conn.Read(ver); err != nil {
		return err
	}

	// Security types: offer None (1)
	conn.Write([]byte{1, 1})
	// Read client's choice
	choice := make([]byte, 1)
	if _, err := conn.Read(choice); err != nil {
		return err
	}
	// SecurityResult: OK
	binary.Write(conn, binary.BigEndian, uint32(0))

	// Read ClientInit (shared flag)
	clientInit := make([]byte, 1)
	if _, err := conn.Read(clientInit); err != nil {
		return err
	}

	// ServerInit
	binary.Write(conn, binary.BigEndian, width)
	binary.Write(conn, binary.BigEndian, height)
	// pixel format (16 bytes): 32bpp, depth=24, little-endian, true-color
	pfmt := make([]byte, 16)
	pfmt[0] = 32                                // bits-per-pixel
	pfmt[1] = 24                                // depth
	pfmt[2] = 0                                 // big-endian: no
	pfmt[3] = 1                                 // true-color: yes
	binary.BigEndian.PutUint16(pfmt[4:6], 255)  // red-max
	binary.BigEndian.PutUint16(pfmt[6:8], 255)  // green-max
	binary.BigEndian.PutUint16(pfmt[8:10], 255) // blue-max
	pfmt[10] = 16                               // red-shift
	pfmt[11] = 8                                // green-shift
	pfmt[12] = 0                                // blue-shift
	conn.Write(pfmt)
	// name
	name := []byte("test")
	binary.Write(conn, binary.BigEndian, uint32(len(name)))
	conn.Write(name)

	// Read client messages until we get a FramebufferUpdateRequest
	for {
		var msgType uint8
		if err := binary.Read(conn, binary.BigEndian, &msgType); err != nil {
			return err
		}
		switch msgType {
		case 0: // SetPixelFormat
			discard := make([]byte, 19)
			conn.Read(discard)
		case 2: // SetEncodings
			var pad uint8
			var numEnc uint16
			binary.Read(conn, binary.BigEndian, &pad)
			binary.Read(conn, binary.BigEndian, &numEnc)
			encs := make([]byte, numEnc*4)
			conn.Read(encs)
		case 3: // FramebufferUpdateRequest
			req := make([]byte, 9)
			conn.Read(req)

			// Send FramebufferUpdate with one rect of solid red pixels
			conn.Write([]byte{0, 0})                        // type=0, padding
			binary.Write(conn, binary.BigEndian, uint16(1)) // numRects
			binary.Write(conn, binary.BigEndian, uint16(0)) // x
			binary.Write(conn, binary.BigEndian, uint16(0)) // y
			binary.Write(conn, binary.BigEndian, width)     // w
			binary.Write(conn, binary.BigEndian, height)    // h
			binary.Write(conn, binary.BigEndian, int32(0))  // encoding: RAW

			// pixels: BGRA (B=0, G=0, R=255, A=0) per our pixel format (R at shift 16)
			for i := 0; i < int(width)*int(height); i++ {
				conn.Write([]byte{0, 0, 255, 0}) // B=0, G=0, R=255 at byte[2] (shift 16/8=byte 2), padding
			}
			return nil
		default:
			return nil
		}
	}
}
