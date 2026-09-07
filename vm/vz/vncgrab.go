package vz

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"time"
)

// RFB encoding numbers sent in SetEncodings. RAW is the only real encoding
// this client decodes; the negative values are pseudo-encodings that declare
// client capabilities. Apple's builtin Virtualization.framework VNC backend
// logs "FIXME IF: It is unclear if we can support clients that don't support
// this pseudo encoding" and SIGTRAPs the host process when a client omits
// them, so advertising these is load-bearing, not decorative.
const (
	encRaw                  int32 = 0
	encPseudoDesktopSize    int32 = -223
	encPseudoLastRect       int32 = -224
	encPseudoExtDesktopSize int32 = -308
)

var clientEncodings = []int32{encRaw, encPseudoDesktopSize, encPseudoExtDesktopSize, encPseudoLastRect}

// VNCGrabFrame connects to a VNC server at addr, performs a minimal RFB
// handshake (no auth), requests a single full framebuffer update, and
// returns the result as PNG bytes. The server must support RAW encoding
// and SecurityType None.
func VNCGrabFrame(addr string, timeout time.Duration) ([]byte, error) {
	return VNCGrabFrameTrace(addr, timeout, nil)
}

// VNCGrabFrameTrace is VNCGrabFrame with a per-step trace callback so a
// crash or protocol error can be localized to the exact RFB message that
// preceded it. tracef may be nil.
func VNCGrabFrameTrace(addr string, timeout time.Duration, tracef func(format string, args ...any)) ([]byte, error) {
	trace := tracef
	if trace == nil {
		trace = func(string, ...any) {}
	}

	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		trace("dial %s: %v", addr, err)
		return nil, fmt.Errorf("vnc dial: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	trace("dial ok: %s -> %s", conn.LocalAddr(), conn.RemoteAddr())

	version, err := rfbReadVersion(conn)
	if err != nil {
		trace("read server version: %v", err)
		return nil, err
	}
	trace("server version: %q, echoing it back", string(bytes.TrimRight(version, "\n")))
	if _, err := conn.Write(version); err != nil {
		return nil, fmt.Errorf("vnc write version: %w", err)
	}

	if err := rfbSecurityNone(conn, trace); err != nil {
		trace("security handshake: %v", err)
		return nil, err
	}

	width, height, err := rfbClientInit(conn, trace)
	if err != nil {
		trace("ClientInit/ServerInit: %v", err)
		return nil, err
	}
	trace("framebuffer: %dx%d", width, height)

	if err := rfbSetPixelFormat(conn); err != nil {
		return nil, err
	}
	trace("sent SetPixelFormat (32bpp depth24 little-endian truecolor)")

	if err := rfbSetEncodings(conn, clientEncodings); err != nil {
		return nil, err
	}
	trace("sent SetEncodings %v", clientEncodings)

	if err := rfbRequestFullUpdate(conn, width, height); err != nil {
		return nil, err
	}
	trace("sent FramebufferUpdateRequest (full, %dx%d)", width, height)

	img, err := rfbReadUpdate(conn, width, height, trace)
	if err != nil {
		trace("read update: %v", err)
		return nil, err
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("vnc png encode: %w", err)
	}
	trace("encoded PNG: %d bytes", buf.Len())
	return buf.Bytes(), nil
}

func rfbReadVersion(r io.Reader) ([]byte, error) {
	ver := make([]byte, 12)
	if _, err := io.ReadFull(r, ver); err != nil {
		return nil, fmt.Errorf("vnc read version: %w", err)
	}
	return ver, nil
}

func rfbSecurityNone(rw io.ReadWriter, trace func(format string, args ...any)) error {
	var numTypes uint8
	if err := binary.Read(rw, binary.BigEndian, &numTypes); err != nil {
		return fmt.Errorf("vnc read security types count: %w", err)
	}
	if numTypes == 0 {
		return fmt.Errorf("vnc server sent zero security types (connection refused)")
	}
	types := make([]byte, numTypes)
	if _, err := io.ReadFull(rw, types); err != nil {
		return fmt.Errorf("vnc read security types: %w", err)
	}
	trace("server security types: %v (selecting None=1)", types)
	hasNone := false
	for _, t := range types {
		if t == 1 {
			hasNone = true
			break
		}
	}
	if !hasNone {
		return fmt.Errorf("vnc server does not support SecurityNone (types: %v)", types)
	}
	if _, err := rw.Write([]byte{1}); err != nil {
		return fmt.Errorf("vnc select security type: %w", err)
	}
	var result uint32
	if err := binary.Read(rw, binary.BigEndian, &result); err != nil {
		return fmt.Errorf("vnc read security result: %w", err)
	}
	if result != 0 {
		return fmt.Errorf("vnc security handshake failed (result=%d)", result)
	}
	return nil
}

func rfbClientInit(rw io.ReadWriter, trace func(format string, args ...any)) (uint16, uint16, error) {
	if _, err := rw.Write([]byte{1}); err != nil {
		return 0, 0, fmt.Errorf("vnc ClientInit: %w", err)
	}
	var width, height uint16
	if err := binary.Read(rw, binary.BigEndian, &width); err != nil {
		return 0, 0, fmt.Errorf("vnc read width: %w", err)
	}
	if err := binary.Read(rw, binary.BigEndian, &height); err != nil {
		return 0, 0, fmt.Errorf("vnc read height: %w", err)
	}
	// rest of ServerInit: pixel format (16 bytes) + name length (4) + name
	var pfmt [16]byte
	if _, err := io.ReadFull(rw, pfmt[:]); err != nil {
		return 0, 0, fmt.Errorf("vnc read pixel format: %w", err)
	}
	trace("server pixel format: bpp=%d depth=%d bigEndian=%d trueColor=%d raw=%x",
		pfmt[0], pfmt[1], pfmt[2], pfmt[3], pfmt)
	var nameLen uint32
	if err := binary.Read(rw, binary.BigEndian, &nameLen); err != nil {
		return 0, 0, fmt.Errorf("vnc read name length: %w", err)
	}
	if nameLen > 0 {
		name := make([]byte, nameLen)
		if _, err := io.ReadFull(rw, name); err != nil {
			return 0, 0, fmt.Errorf("vnc read name: %w", err)
		}
		trace("server desktop name: %q", string(name))
	}
	return width, height, nil
}

func rfbSetPixelFormat(w io.Writer) error {
	// SetPixelFormat message: type=0, 3 padding bytes, then 16-byte pixel format
	msg := make([]byte, 20)
	msg[0] = 0 // message type: SetPixelFormat
	// pixel format at offset 4:
	msg[4] = 32                                 // bits-per-pixel
	msg[5] = 24                                 // depth
	msg[6] = 0                                  // big-endian: no
	msg[7] = 1                                  // true-color: yes
	binary.BigEndian.PutUint16(msg[8:10], 255)  // red-max
	binary.BigEndian.PutUint16(msg[10:12], 255) // green-max
	binary.BigEndian.PutUint16(msg[12:14], 255) // blue-max
	msg[14] = 16                                // red-shift
	msg[15] = 8                                 // green-shift
	msg[16] = 0                                 // blue-shift
	// 3 padding bytes at 17-19
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("vnc SetPixelFormat: %w", err)
	}
	return nil
}

func rfbSetEncodings(w io.Writer, encodings []int32) error {
	msg := make([]byte, 4+4*len(encodings))
	msg[0] = 2 // message type: SetEncodings
	// msg[1] padding
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(encodings)))
	for i, e := range encodings {
		binary.BigEndian.PutUint32(msg[4+i*4:8+i*4], uint32(e))
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("vnc SetEncodings: %w", err)
	}
	return nil
}

func rfbRequestFullUpdate(w io.Writer, width, height uint16) error {
	msg := make([]byte, 10)
	msg[0] = 3 // message type: FramebufferUpdateRequest
	msg[1] = 0 // incremental: no
	// x=0, y=0 at bytes 2-5
	binary.BigEndian.PutUint16(msg[6:8], width)
	binary.BigEndian.PutUint16(msg[8:10], height)
	_, err := w.Write(msg)
	if err != nil {
		return fmt.Errorf("vnc FramebufferUpdateRequest: %w", err)
	}
	return nil
}

func rfbReadUpdate(r io.Reader, fbWidth, fbHeight uint16, trace func(format string, args ...any)) (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, int(fbWidth), int(fbHeight)))

	var msgType uint8
	if err := binary.Read(r, binary.BigEndian, &msgType); err != nil {
		return nil, fmt.Errorf("vnc read update type: %w", err)
	}
	if msgType != 0 {
		return nil, fmt.Errorf("vnc unexpected message type %d (want FramebufferUpdate=0)", msgType)
	}
	var padding uint8
	binary.Read(r, binary.BigEndian, &padding)
	var numRects uint16
	if err := binary.Read(r, binary.BigEndian, &numRects); err != nil {
		return nil, fmt.Errorf("vnc read num rects: %w", err)
	}
	trace("FramebufferUpdate: %d rects", numRects)

	for i := 0; i < int(numRects); i++ {
		var x, y, w, h uint16
		var enc int32
		binary.Read(r, binary.BigEndian, &x)
		binary.Read(r, binary.BigEndian, &y)
		binary.Read(r, binary.BigEndian, &w)
		binary.Read(r, binary.BigEndian, &h)
		if err := binary.Read(r, binary.BigEndian, &enc); err != nil {
			return nil, fmt.Errorf("vnc read rect header: %w", err)
		}
		trace("rect %d/%d: x=%d y=%d w=%d h=%d enc=%d", i+1, numRects, x, y, w, h, enc)
		switch enc {
		case encRaw:
			pixels := make([]byte, int(w)*int(h)*4)
			if _, err := io.ReadFull(r, pixels); err != nil {
				return nil, fmt.Errorf("vnc read rect pixels: %w", err)
			}
			for py := 0; py < int(h); py++ {
				for px := 0; px < int(w); px++ {
					srcOff := (py*int(w) + px) * 4
					dstX := int(x) + px
					dstY := int(y) + py
					if dstX < int(fbWidth) && dstY < int(fbHeight) {
						dstOff := dstY*img.Stride + dstX*4
						// pixel format: R at shift 16, G at shift 8, B at shift 0
						img.Pix[dstOff] = pixels[srcOff+2]   // R
						img.Pix[dstOff+1] = pixels[srcOff+1] // G
						img.Pix[dstOff+2] = pixels[srcOff]   // B
						img.Pix[dstOff+3] = 255              // A
					}
				}
			}
		case encPseudoLastRect:
			trace("LastRect pseudo-rect: stopping rect processing")
			return img, nil
		case encPseudoDesktopSize:
			// No payload; w,h announce the new framebuffer size. This frame
			// was captured at the old size; the next grab picks up the new one.
			trace("DesktopSize pseudo-rect: server resized to %dx%d", w, h)
		case encPseudoExtDesktopSize:
			// Payload: number-of-screens byte, 3 padding bytes, 16 bytes per screen.
			var hdr [4]byte
			if _, err := io.ReadFull(r, hdr[:]); err != nil {
				return nil, fmt.Errorf("vnc read ExtendedDesktopSize header: %w", err)
			}
			if _, err := io.CopyN(io.Discard, r, int64(hdr[0])*16); err != nil {
				return nil, fmt.Errorf("vnc read ExtendedDesktopSize screens: %w", err)
			}
			trace("ExtendedDesktopSize pseudo-rect: %dx%d, %d screens", w, h, hdr[0])
		default:
			return nil, fmt.Errorf("vnc unsupported encoding %d in rect %d", enc, i)
		}
	}
	return img, nil
}
