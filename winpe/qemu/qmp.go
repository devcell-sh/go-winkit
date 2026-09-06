package qemu

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// qmpHandshake dials the QMP socket and negotiates capabilities.
func qmpHandshake(socketPath string, deadline time.Duration) (net.Conn, *json.Encoder, *json.Decoder, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("QMP connect: %w", err)
	}
	conn.SetDeadline(time.Now().Add(deadline))

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var greeting map[string]any
	if err := dec.Decode(&greeting); err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("QMP greeting: %w", err)
	}
	if err := enc.Encode(map[string]string{"execute": "qmp_capabilities"}); err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("QMP capabilities: %w", err)
	}
	var capResp map[string]any
	if err := dec.Decode(&capResp); err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("QMP capabilities response: %w", err)
	}
	return conn, enc, dec, nil
}

// QMPScreendump captures the QEMU display framebuffer to a PPM file.
func QMPScreendump(socketPath, outputFile string) error {
	conn, enc, dec, err := qmpHandshake(socketPath, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	cmd := map[string]any{
		"execute":   "screendump",
		"arguments": map[string]string{"filename": outputFile},
	}
	if err := enc.Encode(cmd); err != nil {
		return fmt.Errorf("QMP screendump: %w", err)
	}
	var resp map[string]any
	if err := dec.Decode(&resp); err != nil {
		return fmt.Errorf("QMP screendump response: %w", err)
	}
	if errObj, ok := resp["error"]; ok {
		return fmt.Errorf("QMP screendump error: %v", errObj)
	}
	return nil
}

// QMPQuit sends the "quit" command which flushes block caches and exits.
func QMPQuit(socketPath string) error {
	conn, enc, _, err := qmpHandshake(socketPath, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	return enc.Encode(map[string]string{"execute": "quit"})
}

// QMPSendKeys presses the given QEMU qcodes together as one chord and releases
// them, holding for holdMs milliseconds (0 lets QEMU pick its default). Use it
// for shortcuts like Ctrl+Alt+Del ("ctrl","alt","delete") or opening Run with
// the Windows key ("meta_l","r"). Qcode names are QEMU's, not characters — see
// qcodeForRune for the printable set; modifiers are "ctrl","alt","shift",
// "meta_l"; navigation is "ret","tab","spc","esc","up","down","left","right".
func QMPSendKeys(socketPath string, holdMs int, qcodes ...string) error {
	conn, enc, dec, err := qmpHandshake(socketPath, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	return sendKeyChord(enc, dec, holdMs, qcodes)
}

// QMPTypeText types an ASCII string into the guest one character at a time,
// holding shift for uppercase and shifted symbols. Unmappable runes are
// skipped. A per-key delay lets the guest keyboard buffer keep up; delayMs=0
// applies a small default.
func QMPTypeText(socketPath string, text string, delayMs int) error {
	if delayMs <= 0 {
		delayMs = 30
	}
	conn, enc, dec, err := qmpHandshake(socketPath, 30*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	for _, r := range text {
		qcode, shifted, ok := qcodeForRune(r)
		if !ok {
			continue
		}
		chord := []string{qcode}
		if shifted {
			chord = []string{"shift", qcode}
		}
		if err := sendKeyChord(enc, dec, 0, chord); err != nil {
			return err
		}
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
	}
	return nil
}

// sendKeyChord issues one send-key command over an established QMP connection.
func sendKeyChord(enc *json.Encoder, dec *json.Decoder, holdMs int, qcodes []string) error {
	keys := make([]map[string]string, len(qcodes))
	for i, q := range qcodes {
		keys[i] = map[string]string{"type": "qcode", "data": q}
	}
	args := map[string]any{"keys": keys}
	if holdMs > 0 {
		args["hold-time"] = holdMs
	}
	if err := enc.Encode(map[string]any{"execute": "send-key", "arguments": args}); err != nil {
		return fmt.Errorf("QMP send-key: %w", err)
	}
	var resp map[string]any
	if err := dec.Decode(&resp); err != nil {
		return fmt.Errorf("QMP send-key response: %w", err)
	}
	if errObj, ok := resp["error"]; ok {
		return fmt.Errorf("QMP send-key error: %v", errObj)
	}
	return nil
}

// qcodeForRune maps a printable ASCII rune to its QEMU qcode and whether shift
// must be held. It covers the US layout: letters, digits, space, and the common
// punctuation a diagnostic PowerShell line needs (paths, quotes, redirection).
func qcodeForRune(r rune) (qcode string, shifted bool, ok bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return string(r), false, true
	case r >= 'A' && r <= 'Z':
		return string(r - 'A' + 'a'), true, true
	case r >= '1' && r <= '9':
		return string(r), false, true
	case r == '0':
		return "0", false, true
	}
	if q, ok := unshiftedPunct[r]; ok {
		return q, false, true
	}
	if q, ok := shiftedPunct[r]; ok {
		return q, true, true
	}
	return "", false, false
}

// unshiftedPunct and shiftedPunct map punctuation to QEMU qcodes. Split so the
// caller knows whether to hold shift.
var unshiftedPunct = map[rune]string{
	' ': "spc", '\n': "ret", '\t': "tab",
	'-': "minus", '=': "equal", '[': "bracket_left", ']': "bracket_right",
	'\\': "backslash", ';': "semicolon", '\'': "apostrophe", '`': "grave_accent",
	',': "comma", '.': "dot", '/': "slash",
}

var shiftedPunct = map[rune]string{
	'!': "1", '@': "2", '#': "3", '$': "4", '%': "5", '^': "6", '&': "7",
	'*': "8", '(': "9", ')': "0", '_': "minus", '+': "equal",
	'{': "bracket_left", '}': "bracket_right", '|': "backslash",
	':': "semicolon", '"': "apostrophe", '~': "grave_accent",
	'<': "comma", '>': "dot", '?': "slash",
}
