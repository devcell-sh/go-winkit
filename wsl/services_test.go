package wsl

import (
	"strings"
	"testing"

	"github.com/devcell-sh/go-winkit/s6"
	"github.com/devcell-sh/go-winkit/sftpshare"
)

func testService(name string) s6.Service {
	return s6.Service{Name: name, Run: "#!/bin/sh\nexec " + name + "\n"}
}

func TestBaseRecipeSeedsSSHD(t *testing.T) {
	r, err := BaseRecipe("alpine", "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	run := string(r.ContextFiles["s6/sshd/run"])
	if !strings.Contains(run, "command -v sshd") {
		t.Errorf("base sshd run must tolerate sshd outside PATH, got %q", run)
	}
}

func TestNixRecipeSeedsSSHD(t *testing.T) {
	r, err := NixRecipe("dev", "winkit", "")
	if err != nil {
		t.Fatal(err)
	}
	run := string(r.ContextFiles["s6/sshd/run"])
	if !strings.Contains(run, "-f /etc/ssh/sshd_config") {
		t.Errorf("nix sshd run must pin the config path, got %q", run)
	}
}

// TestRecipeAddUserService is the library contract for additional user
// services: they land under the reserved s6/ context prefix, extra files
// included, on a recipe that already carries built-ins.
func TestRecipeAddUserService(t *testing.T) {
	r, err := BaseRecipe("alpine", "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	svc := testService("myagent")
	svc.Files = map[string][]byte{"finish": []byte("#!/bin/sh\nexit 0\n")}
	if err := r.AddService(svc); err != nil {
		t.Fatal(err)
	}
	if string(r.ContextFiles["s6/myagent/run"]) != svc.Run {
		t.Errorf("run not in context: %q", r.ContextFiles["s6/myagent/run"])
	}
	if len(r.ContextFiles["s6/myagent/finish"]) == 0 {
		t.Error("finish not in context")
	}
	// The built-in must be untouched.
	if len(r.ContextFiles["s6/sshd/run"]) == 0 {
		t.Error("sshd built-in lost")
	}
}

func TestAddServiceCollision(t *testing.T) {
	r, err := BaseRecipe("alpine", "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddService(testService("sshd")); err == nil || !strings.Contains(err.Error(), "PutService") {
		t.Fatalf("adding sshd over the built-in must error, got %v", err)
	}
	if err := r.AddService(testService("x")); err != nil {
		t.Fatal(err)
	}
	if err := r.AddService(testService("x")); err == nil {
		t.Fatal("duplicate AddService must error")
	}
	// A hand-set ContextFiles key under the prefix counts as occupied.
	r.ContextFiles["s6/squat/data"] = []byte("x")
	if err := r.AddService(testService("squat")); err == nil {
		t.Fatal("squatted prefix must error")
	}
}

func TestPutServiceOverridesBuiltin(t *testing.T) {
	r, err := BaseRecipe("alpine", "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	// Give the built-in an extra file to prove the override removes it.
	r.ContextFiles["s6/sshd/stale"] = []byte("old")
	custom := s6.Service{Name: "sshd", Run: "#!/bin/sh\nexec sshd -D -p 2299\n"}
	if err := r.PutService(custom); err != nil {
		t.Fatal(err)
	}
	if got := string(r.ContextFiles["s6/sshd/run"]); got != custom.Run {
		t.Errorf("override not applied: %q", got)
	}
	if _, ok := r.ContextFiles["s6/sshd/stale"]; ok {
		t.Error("stale file survived PutService")
	}
}

func TestServiceChangesCacheKey(t *testing.T) {
	recipe := func(mut func(*Recipe)) string {
		r, err := BaseRecipe("alpine", "dev", "")
		if err != nil {
			t.Fatal(err)
		}
		if mut != nil {
			mut(&r)
		}
		return cachePath("/c", r)
	}
	base := recipe(nil)
	added := recipe(func(r *Recipe) { r.AddService(testService("extra")) })
	edited := recipe(func(r *Recipe) {
		r.PutService(s6.Service{Name: "extra", Run: "#!/bin/sh\nexec other\n"})
	})
	overridden := recipe(func(r *Recipe) {
		r.PutService(s6.Service{Name: "sshd", Run: "#!/bin/sh\nexec sshd -D -p 2299\n"})
	})
	for name, p := range map[string]string{"added": added, "edited": edited, "overridden": overridden} {
		if p == base {
			t.Errorf("%s service did not change the cache key", name)
		}
	}
	if added == edited {
		t.Error("different run content produced the same cache key")
	}
}

func TestDistroServiceOnPrebuiltErrors(t *testing.T) {
	for _, image := range []string{"https://example.com/x.wsl", "./local.wsl"} {
		d, err := DistroFor(image, "dev", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := d.AddService(testService("x")); err == nil || !strings.Contains(err.Error(), "prebuilt") {
			t.Errorf("%s: want prebuilt error, got %v", image, err)
		}
		if err := d.PutService(testService("x")); err == nil {
			t.Errorf("%s: PutService must error too", image)
		}
	}
}

func TestDistroAddServiceOnRecipe(t *testing.T) {
	d, err := DistroFor("alpine", "dev", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddService(testService("myagent")); err != nil {
		t.Fatal(err)
	}
	if len(d.Recipe.ContextFiles["s6/myagent/run"]) == 0 {
		t.Error("service not applied to underlying recipe")
	}
}

// TestRcloneMountService: the mount module renders the build-time share
// parameters into the interop run script and paces restarts via finish.
func TestRcloneMountService(t *testing.T) {
	svc, err := RcloneMountService("10.0.2.2", 9844, "winkit", "p'w", "W", "winkit")
	if err != nil {
		t.Fatal(err)
	}
	if svc.Name != "rclone-mount" {
		t.Errorf("name = %q", svc.Name)
	}
	for _, want := range []string{
		"host=10.0.2.2,port=9844,user=winkit",
		`'W:'`,
		`--volname 'winkit'`,
		"obscure",    // password obscured at exec time
		`'p'\''w'`,   // shell-quoted password
		"rclone.exe", // Windows binary via interop
		"FileSecurity=D:P(A;;FA;;;WD)",
		"--vfs-cache-mode writes",
	} {
		if !strings.Contains(svc.Run, want) {
			t.Errorf("run missing %q:\n%s", want, svc.Run)
		}
	}
	if strings.Contains(svc.Run, "@") {
		t.Errorf("unsubstituted token left in run:\n%s", svc.Run)
	}
	if finish := string(svc.Files["finish"]); !strings.Contains(finish, "sleep") {
		t.Errorf("finish must pace restarts, got %q", finish)
	}
}

func TestRcloneMountServiceValidatesInputs(t *testing.T) {
	if _, err := RcloneMountService("", 9844, "u", "p", "W", "v"); err == nil {
		t.Error("empty host must error")
	}
	if _, err := RcloneMountService("h", 0, "u", "p", "W", "v"); err == nil {
		t.Error("zero port must error")
	}
}

func TestDirShareService(t *testing.T) {
	svc, err := DirShareService([]sftpshare.DirShare{
		{Drive: "X", VMPath: "/work/src"},
		{Drive: "Y", VMPath: "/work/data"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`ln -sf "/mnt/x" "/work/src"`, `ln -sf "/mnt/y" "/work/data"`, "exec sleep infinity"} {
		if !strings.Contains(svc.Run, want) {
			t.Errorf("dirshare run missing %q:\n%s", want, svc.Run)
		}
	}
}
