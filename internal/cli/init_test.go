package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCmd_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	cmd := newInitCmd()
	cmd.SetArgs([]string{})
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "winkit.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "from: windows/11-pro-arm64") {
		t.Fatal("generated file missing from: line")
	}
	if !strings.Contains(out.String(), "Created winkit.yaml") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestInitCmd_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	os.WriteFile("winkit.yaml", []byte("existing"), 0o644)

	cmd := newInitCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when file exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v", err)
	}
}

func TestInitCmd_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	os.WriteFile("winkit.yaml", []byte("old"), 0o644)

	cmd := newInitCmd()
	cmd.SetArgs([]string{"--force"})
	var out strings.Builder
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile("winkit.yaml")
	if strings.Contains(string(data), "old") {
		t.Fatal("file not overwritten")
	}
}
