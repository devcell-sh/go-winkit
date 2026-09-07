// Package s6 models s6 service directories: the unit of supervision under
// an s6-svscan scan dir (in winkit's WSL distro, /etc/s6/services). A
// Service can be loaded from a directory laid out like a scan dir entry,
// materialized back to disk, or flattened into build-context file maps.
//
// The package is deliberately free of wsl/docker specifics so other
// consumers (devcell's fragment replacement) can import it directly.
package s6

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// nameRe: a service name is a single path element; s6-svscan treats every
// subdirectory of the scan dir as a service, so anything directory-safe
// and dot/dash-friendly is allowed, but no separators.
var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// Service is one s6 service directory for an s6-svscan scan dir.
type Service struct {
	// Name is the directory name under the scan dir.
	Name string
	// Run is the run script content. s6-supervise execve()s it directly,
	// so it must start with a #! interpreter line.
	Run string
	// Files are extra files relative to the service dir (finish, data/…).
	// The key "run" is reserved for Run.
	Files map[string][]byte
}

// Validate checks the service is materializable and supervisable.
func (s Service) Validate() error {
	if !nameRe.MatchString(s.Name) {
		return fmt.Errorf("s6: invalid service name %q", s.Name)
	}
	if strings.TrimSpace(s.Run) == "" {
		return fmt.Errorf("s6: service %s: empty run script", s.Name)
	}
	if !strings.HasPrefix(s.Run, "#!") {
		// Without a shebang s6-supervise's execve fails and the service
		// enters a silent death loop; catch it at build time.
		return fmt.Errorf("s6: service %s: run script must start with #!", s.Name)
	}
	for rel := range s.Files {
		if rel == "run" {
			return fmt.Errorf("s6: service %s: Files[%q] collides with Run", s.Name, rel)
		}
		if rel == "" || path.Clean(rel) != rel || strings.HasPrefix(rel, "/") || rel == ".." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("s6: service %s: invalid file path %q", s.Name, rel)
		}
	}
	return nil
}

// ContextFiles flattens the service into file-map entries keyed by
// prefix + "<name>/<rel>" (slash-separated), the shape of a docker build
// context map. Run is normalized to LF line endings.
func (s Service) ContextFiles(prefix string) (map[string][]byte, error) {
	n := s.normalized()
	if err := n.Validate(); err != nil {
		return nil, err
	}
	out := map[string][]byte{prefix + n.Name + "/run": []byte(n.Run)}
	for rel, data := range n.Files {
		out[prefix+n.Name+"/"+rel] = data
	}
	return out, nil
}

// WriteDir materializes the service under scanDir: run (and a top-level
// finish, if present) are written 0755, everything else 0644. Intended for
// runtime scan dirs and tests; docker builds go through ContextFiles.
func (s Service) WriteDir(scanDir string) error {
	n := s.normalized()
	if err := n.Validate(); err != nil {
		return err
	}
	dir := filepath.Join(scanDir, n.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "run"), []byte(n.Run), 0o755); err != nil {
		return err
	}
	for rel, data := range n.Files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if rel == "finish" {
			mode = 0o755
		}
		if err := os.WriteFile(p, data, mode); err != nil {
			return err
		}
	}
	return nil
}

// normalized returns a copy with the run script's line endings forced to
// LF: a \r after the shebang breaks execve under s6-supervise, and run
// scripts frequently arrive from Windows checkouts.
func (s Service) normalized() Service {
	s.Run = strings.ReplaceAll(s.Run, "\r\n", "\n")
	return s
}

// FromDir loads one service from a directory laid out like a scan dir
// entry: it must contain a "run" file; everything else becomes Files.
func FromDir(name, dir string) (Service, error) {
	return FromFS(os.DirFS(dir), ".", name)
}

// LoadDir loads every subdirectory of dir as a Service — dir has the
// on-disk shape of an s6 scan dir. Non-directory entries and an empty
// dir are errors (a scan dir with stray files is almost always a
// misconfigured path).
func LoadDir(dir string) ([]Service, error) {
	return LoadFS(os.DirFS(dir), ".")
}

// LoadFS is LoadDir over an fs.FS rooted at root, for embedded catalogs.
// Services are returned sorted by name.
func LoadFS(fsys fs.FS, root string) ([]Service, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("s6: reading services dir: %w", err)
	}
	var out []Service
	for _, e := range entries {
		if !e.IsDir() {
			return nil, fmt.Errorf("s6: services dir contains non-directory entry %q (each subdirectory is one service)", e.Name())
		}
		svc, err := FromFS(fsys, path.Join(root, e.Name()), e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, svc)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("s6: services dir is empty")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// FromFS loads a single service dir rooted at root within fsys (the fs.FS
// counterpart of FromDir, used for embedded catalogs).
func FromFS(fsys fs.FS, root, name string) (Service, error) {
	svc := Service{Name: name, Files: map[string][]byte{}}
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		if root == "." {
			rel = p
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if rel == "run" {
			svc.Run = string(data)
			return nil
		}
		svc.Files[rel] = data
		return nil
	})
	if err != nil {
		return Service{}, fmt.Errorf("s6: loading service %s: %w", name, err)
	}
	if svc.Run == "" {
		return Service{}, fmt.Errorf("s6: service %s: missing run script", name)
	}
	if len(svc.Files) == 0 {
		svc.Files = nil
	}
	svc = svc.normalized()
	if err := svc.Validate(); err != nil {
		return Service{}, err
	}
	return svc, nil
}
