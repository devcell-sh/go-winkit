//go:build wimlib

package winpe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	regedit "github.com/devcell-sh/go-regedit"
	"github.com/devcell-sh/go-wimlib"
)

// InspectFeatures probes a WIM image for the capabilities winkit cares about,
// grouped for display. imageNum selects the image (2 = the WinPE boot.wim
// convention; 1 = install.wim's first edition). Missing pieces are reported,
// never fatal — the whole point is to show what an image does and does not
// carry.
func InspectFeatures(wimPath string, imageNum int) ([]FeatureGroup, error) {
	w, err := wimlib.OpenWIM(wimPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", wimPath, err)
	}
	defer w.Close()

	img := imageNum
	listSet := func(dir string) map[string]bool {
		out := map[string]bool{}
		kids, err := w.ListChildren(img, dir)
		if err != nil {
			return out
		}
		for _, k := range kids {
			out[strings.ToLower(filepath.Base(k))] = true
		}
		return out
	}
	sys32 := listSet(`\Windows\System32`)
	winkitDir := listSet(`\winkit`)
	pwsh := listSet(`\winkit\pwsh`)

	tmp, err := os.MkdirTemp("", "winkit-features-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	_ = w.ExtractPaths(img, tmp, []string{
		`\Windows\System32\config\SYSTEM`,
	})
	sysHive := filepath.Join(tmp, "Windows", "System32", "config", "SYSTEM")

	inSys := func(name string) Feature {
		return Feature{Name: name, Present: sys32[strings.ToLower(name)]}
	}
	inSet := func(name string, set map[string]bool) Feature {
		return Feature{Name: name, Present: set[strings.ToLower(name)]}
	}
	startNames := map[byte]string{0: "boot", 1: "system", 2: "auto", 3: "manual", 4: "disabled"}
	svc := func(name string) Feature {
		f := Feature{Name: name + " service"}
		k, err := regedit.ReadServiceKey(sysHive, `ControlSet001\Services\`+name)
		if err != nil {
			return f
		}
		f.Present = true
		if v, ok := k.Values["Start"]; ok && len(v.Data) >= 1 {
			if s, ok := startNames[v.Data[0]]; ok {
				f.Detail = "Start=" + s
			}
		}
		return f
	}

	return []FeatureGroup{
		{Name: "winkit payload", Items: []Feature{
			inSet("gosshd.exe", winkitDir),
			inSet("gosshd.cmd", winkitDir),
			{Name: "winpeshl.ini", Present: sys32["winpeshl.ini"]},
		}},
		{Name: "PowerShell", Items: []Feature{
			inSet("pwsh.exe", pwsh),
		}},
		{Name: "Event Log & ETW", Items: []Feature{
			inSys("wevtsvc.dll"),
			inSys("wevtapi.dll"),
			inSys("wevtutil.exe"),
			svc("EventLog"),
		}},
	}, nil
}

// ListImageDir returns the children of dir within a WIM image (browse mode for
// list-features --path). dir uses backslash separators, e.g.
// `\Windows\System32\winevt\Logs`.
func ListImageDir(wimPath string, imageNum int, dir string) ([]string, error) {
	w, err := wimlib.OpenWIM(wimPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", wimPath, err)
	}
	defer w.Close()
	return w.ListChildren(imageNum, dir)
}
