// Package vmstate tracks running winkit VMs via JSON state files.
// Each VM gets a file named <name>.json in a state directory
// (default ~/.winkit/run/).
package vmstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// State describes a running VM.
type State struct {
	Name      string    `json:"name"`
	ImagePath string    `json:"image_path"`
	PID       int       `json:"pid"`
	Backend   string    `json:"backend"`
	StartedAt time.Time `json:"started_at"`
	SSHPort   uint16    `json:"ssh_port,omitempty"`
	RDPPort   uint16    `json:"rdp_port,omitempty"`
	VNCPort   uint16    `json:"vnc_port,omitempty"`
	Accel     string    `json:"accel,omitempty"`
	OutputDir string    `json:"output_dir,omitempty"`
}

// DefaultDir returns the default state directory (~/.winkit/run/).
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	return filepath.Join(home, ".winkit", "run")
}

// Save writes the state to <dir>/<name>.json. Creates dir if needed.
func Save(dir string, s *State) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}
	return os.WriteFile(stateFile(dir, s.Name), data, 0o644)
}

// Load reads the state for name from <dir>/<name>.json.
func Load(dir, name string) (*State, error) {
	data, err := os.ReadFile(stateFile(dir, name))
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state %s: %w", name, err)
	}
	return &s, nil
}

// List returns all states in dir. Returns nil (not error) if dir doesn't exist.
func List(dir string) ([]*State, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var states []*State
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		s, err := Load(dir, name)
		if err != nil {
			continue
		}
		states = append(states, s)
	}
	return states, nil
}

// Remove deletes the state file for name. No error if it doesn't exist.
func Remove(dir, name string) error {
	err := os.Remove(stateFile(dir, name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// FindByImage returns the first state whose ImagePath matches path, or nil.
func FindByImage(dir, path string) (*State, error) {
	states, err := List(dir)
	if err != nil {
		return nil, err
	}
	for _, s := range states {
		if s.ImagePath == path {
			return s, nil
		}
	}
	return nil, nil
}

// IsAlive checks if a process with the given PID is still running.
func IsAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil
}

func stateFile(dir, name string) string {
	return filepath.Join(dir, name+".json")
}
