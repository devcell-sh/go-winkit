package qemu

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestMonitor_FiresOnStallOncePerEpisode(t *testing.T) {
	var polls, stalls atomic.Int64
	signals := []StallSignal{
		{ScreenHash: 1, ReadBytes: 100}, // boot read activity
		{ScreenHash: 1, ReadBytes: 100}, // unchanged 1
		{ScreenHash: 1, ReadBytes: 100}, // unchanged 2 -> stall (threshold 2)
		{ScreenHash: 1, ReadBytes: 100}, // unchanged 3 (same episode, no refire)
		{ScreenHash: 2, ReadBytes: 200}, // progress -> reset
		{ScreenHash: 2, ReadBytes: 200}, // unchanged 1
		{ScreenHash: 2, ReadBytes: 200}, // unchanged 2 -> stall (second episode)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := Monitor(ctx, MonitorConfig{
		Interval:       time.Millisecond,
		StallThreshold: 2,
		Poll: func(ctx context.Context) (StallSignal, error) {
			n := polls.Add(1)
			if int(n) > len(signals) {
				cancel()
				return signals[len(signals)-1], nil
			}
			return signals[n-1], nil
		},
		OnStall: func(consecutive int) {
			stalls.Add(1)
		},
	})
	if err != nil {
		t.Fatalf("Monitor returned error: %v", err)
	}
	if got := stalls.Load(); got != 2 {
		t.Fatalf("OnStall fired %d times, want 2 (once per stall episode)", got)
	}
}

func TestMonitor_ReportsPollErrors(t *testing.T) {
	var sawErr atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := Monitor(ctx, MonitorConfig{
		Interval: time.Millisecond,
		Poll: func(ctx context.Context) (StallSignal, error) {
			cancel()
			return StallSignal{}, os.ErrNotExist
		},
		OnError: func(err error) { sawErr.Store(true) },
	})
	if err != nil {
		t.Fatalf("poll errors must not abort Monitor: %v", err)
	}
	if !sawErr.Load() {
		t.Fatal("OnError was not called for a failing poll")
	}
}

func TestMonitor_RequiresPoll(t *testing.T) {
	if err := Monitor(context.Background(), MonitorConfig{}); err == nil {
		t.Fatal("Monitor without Poll should error")
	}
}

// fakeQMP serves one QMP connection: greeting, capabilities ack, then
// canned responses per execute command.
func fakeQMP(t *testing.T, sock string, respond func(cmd string) any) {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				enc := json.NewEncoder(c)
				dec := json.NewDecoder(c)
				enc.Encode(map[string]any{"QMP": map[string]any{"version": map[string]any{}}})
				var req map[string]any
				if dec.Decode(&req) != nil { // qmp_capabilities
					return
				}
				enc.Encode(map[string]any{"return": map[string]any{}})
				for {
					if dec.Decode(&req) != nil {
						return
					}
					cmd, _ := req["execute"].(string)
					enc.Encode(respond(cmd))
				}
			}(conn)
		}
	}()
}

func TestQMPBlockReadBytes(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "qmp.sock")
	fakeQMP(t, sock, func(cmd string) any {
		if cmd != "query-blockstats" {
			t.Errorf("unexpected QMP command %q", cmd)
		}
		return map[string]any{"return": []any{
			map[string]any{"stats": map[string]any{"rd_bytes": float64(1000)}},
			map[string]any{"stats": map[string]any{"rd_bytes": float64(234)}},
		}}
	})

	n, err := QMPBlockReadBytes(sock)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1234 {
		t.Fatalf("QMPBlockReadBytes = %d, want 1234", n)
	}
}

func TestScreenStatePoller(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "qmp.sock")
	shotDir := filepath.Join(dir, "shots")
	if err := os.MkdirAll(shotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fakeQMP(t, sock, func(cmd string) any {
		switch cmd {
		case "screendump":
			return map[string]any{"return": map[string]any{}}
		case "query-blockstats":
			return map[string]any{"return": []any{
				map[string]any{"stats": map[string]any{"rd_bytes": float64(42)}},
			}}
		default:
			t.Errorf("unexpected QMP command %q", cmd)
			return map[string]any{"error": map[string]any{}}
		}
	})

	// The fake server doesn't write the dump file; pre-create it so the
	// hash step has bytes to read.
	ppm := filepath.Join(shotDir, "monitor.ppm")
	if err := os.WriteFile(ppm, []byte("P6 1 1 255 abc"), 0o644); err != nil {
		t.Fatal(err)
	}

	poll := ScreenStatePoller(sock, shotDir)
	sig, err := poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sig.ReadBytes != 42 {
		t.Fatalf("ReadBytes = %d, want 42", sig.ReadBytes)
	}
	if sig.ScreenHash == 0 {
		t.Fatal("ScreenHash should be computed from the dump file")
	}
}
