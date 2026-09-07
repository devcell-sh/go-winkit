package s6

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func validService() Service {
	return Service{Name: "myagent", Run: "#!/bin/sh\nexec /usr/local/bin/myagent\n"}
}

func TestValidate(t *testing.T) {
	if err := validService().Validate(); err != nil {
		t.Fatalf("valid service rejected: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Service)
		want string
	}{
		{"empty name", func(s *Service) { s.Name = "" }, "invalid service name"},
		{"slash in name", func(s *Service) { s.Name = "a/b" }, "invalid service name"},
		{"dotdot name", func(s *Service) { s.Name = ".." }, "invalid service name"},
		{"leading dash", func(s *Service) { s.Name = "-x" }, "invalid service name"},
		{"empty run", func(s *Service) { s.Run = "" }, "empty run script"},
		{"no shebang", func(s *Service) { s.Run = "exec sshd -D\n" }, "must start with #!"},
		{"files run collision", func(s *Service) { s.Files = map[string][]byte{"run": nil} }, "collides with Run"},
		{"files absolute", func(s *Service) { s.Files = map[string][]byte{"/etc/x": nil} }, "invalid file path"},
		{"files dotdot", func(s *Service) { s.Files = map[string][]byte{"../x": nil} }, "invalid file path"},
		{"files unclean", func(s *Service) { s.Files = map[string][]byte{"a//b": nil} }, "invalid file path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validService()
			tc.mut(&s)
			err := s.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestContextFiles(t *testing.T) {
	s := validService()
	s.Files = map[string][]byte{"finish": []byte("#!/bin/sh\nexit 0\n"), "data/conf": []byte("x=1")}
	got, err := s.ContextFiles("s6/")
	if err != nil {
		t.Fatal(err)
	}
	if string(got["s6/myagent/run"]) != s.Run {
		t.Errorf("run not mapped: %q", got["s6/myagent/run"])
	}
	if string(got["s6/myagent/finish"]) == "" || string(got["s6/myagent/data/conf"]) != "x=1" {
		t.Errorf("extra files not mapped: %v", keys(got))
	}
	if len(got) != 3 {
		t.Errorf("want 3 entries, got %v", keys(got))
	}
}

func TestContextFilesNormalizesCRLF(t *testing.T) {
	s := Service{Name: "win", Run: "#!/bin/sh\r\nexec x\r\n"}
	got, err := s.ContextFiles("s6/")
	if err != nil {
		t.Fatal(err)
	}
	if string(got["s6/win/run"]) != "#!/bin/sh\nexec x\n" {
		t.Errorf("CRLF not normalized: %q", got["s6/win/run"])
	}
}

func TestWriteDirAndFromDirRoundTrip(t *testing.T) {
	scan := t.TempDir()
	s := validService()
	s.Files = map[string][]byte{"finish": []byte("#!/bin/sh\nexit 0\n"), "env/PORT": []byte("2223")}
	if err := s.WriteDir(scan); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(filepath.Join(scan, "myagent", "run"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("run mode = %v, want 0755", fi.Mode().Perm())
	}
	fi, _ = os.Stat(filepath.Join(scan, "myagent", "finish"))
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("finish mode = %v, want 0755", fi.Mode().Perm())
	}
	fi, _ = os.Stat(filepath.Join(scan, "myagent", "env", "PORT"))
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("data file mode = %v, want 0644", fi.Mode().Perm())
	}

	back, err := FromDir("myagent", filepath.Join(scan, "myagent"))
	if err != nil {
		t.Fatal(err)
	}
	if back.Run != s.Run {
		t.Errorf("run round-trip: %q != %q", back.Run, s.Run)
	}
	if string(back.Files["env/PORT"]) != "2223" || string(back.Files["finish"]) == "" {
		t.Errorf("files round-trip: %v", back.Files)
	}
}

// TestLoadDirUserServices is the user-facing contract: a winkit.yaml
// `wsl.services` directory laid out like a scan dir loads every service.
func TestLoadDirUserServices(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("metrics/run", "#!/bin/sh\nexec metrics-agent\n")
	write("myagent/run", "#!/bin/sh\nexec myagent\n")
	write("myagent/finish", "#!/bin/sh\nexit 0\n")
	write("myagent/data/conf", "x=1")

	svcs, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 2 {
		t.Fatalf("want 2 services, got %d", len(svcs))
	}
	// LoadFS sorts by name.
	if svcs[0].Name != "metrics" || svcs[1].Name != "myagent" {
		t.Errorf("names = %s, %s", svcs[0].Name, svcs[1].Name)
	}
	if string(svcs[1].Files["data/conf"]) != "x=1" {
		t.Errorf("nested data file lost: %v", svcs[1].Files)
	}
}

func TestLoadDirErrors(t *testing.T) {
	t.Run("empty dir", func(t *testing.T) {
		if _, err := LoadDir(t.TempDir()); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("want empty-dir error, got %v", err)
		}
	})
	t.Run("stray file", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0o644)
		if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "non-directory") {
			t.Fatalf("want non-directory error, got %v", err)
		}
	})
	t.Run("service without run", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "broken"), 0o755)
		os.WriteFile(filepath.Join(dir, "broken", "finish"), []byte("#!/bin/sh\n"), 0o644)
		if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "missing run script") {
			t.Fatalf("want missing-run error, got %v", err)
		}
	})
	t.Run("run without shebang", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "bad"), 0o755)
		os.WriteFile(filepath.Join(dir, "bad", "run"), []byte("exec x\n"), 0o644)
		if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "must start with #!") {
			t.Fatalf("want shebang error, got %v", err)
		}
	})
}

func TestLoadFS(t *testing.T) {
	fsys := fstest.MapFS{
		"catalog/sshd/run":        {Data: []byte("#!/bin/sh\nexec sshd -D\n")},
		"catalog/agent/run":       {Data: []byte("#!/bin/sh\r\nexec agent\r\n")},
		"catalog/agent/data/conf": {Data: []byte("k=v")},
	}
	svcs, err := LoadFS(fsys, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if len(svcs) != 2 || svcs[0].Name != "agent" || svcs[1].Name != "sshd" {
		t.Fatalf("unexpected services: %+v", svcs)
	}
	if svcs[0].Run != "#!/bin/sh\nexec agent\n" {
		t.Errorf("CRLF not normalized on load: %q", svcs[0].Run)
	}
	if string(svcs[0].Files["data/conf"]) != "k=v" {
		t.Errorf("nested file lost: %v", svcs[0].Files)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
