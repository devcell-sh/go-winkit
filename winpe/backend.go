package winpe

import (
	"fmt"
	"os"
)

// ResolveBackend returns a VMBackend by name using the provided registry.
// An empty name checks WINKIT_VM_BACKEND env, then falls back to the platform
// default (vz on darwin, qemu elsewhere).
func ResolveBackend(name string, registry map[string]VMBackend) (VMBackend, string, error) {
	if name == "" {
		name = os.Getenv("WINKIT_VM_BACKEND")
	}
	if name == "" {
		name = defaultBackendName
	}
	b, ok := registry[name]
	if !ok {
		return nil, name, fmt.Errorf("unknown VM backend %q", name)
	}
	return b, name, nil
}

// DefaultBackendName returns the platform default backend name.
func DefaultBackendName() string {
	return defaultBackendName
}
