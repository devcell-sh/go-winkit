package wslnix

import (
	"fmt"
	"strings"

	"github.com/devcell-sh/go-winkit/sftpshare"
)

// MountScript returns a shell script that creates parent directories and
// symlinks vmPath to the WinFsp drive's WSL1 mount point (/mnt/<drive>).
// The script is idempotent: re-running it replaces an existing symlink.
func MountScript(share sftpshare.DirShare) (string, error) {
	if share.VMPath == "" {
		return "", fmt.Errorf("wslnix: DirShare.VMPath is empty")
	}
	if share.Drive == "" {
		return "", fmt.Errorf("wslnix: DirShare.Drive is empty")
	}
	drive := strings.ToLower(share.Drive)
	mnt := "/mnt/" + drive

	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n")
	fmt.Fprintf(&b, "mkdir -p %q\n", parentDir(share.VMPath))
	fmt.Fprintf(&b, "[ -L %q ] && rm %q\n", share.VMPath, share.VMPath)
	fmt.Fprintf(&b, "ln -sf %q %q\n", mnt, share.VMPath)
	return b.String(), nil
}

// MountScriptMulti returns a shell script that mounts all shares.
func MountScriptMulti(shares []sftpshare.DirShare) (string, error) {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n")
	for _, s := range shares {
		script, err := MountScript(s)
		if err != nil {
			return "", err
		}
		lines := strings.SplitAfter(script, "\n")
		for _, l := range lines {
			if l == "#!/bin/sh\n" || l == "set -e\n" || l == "" {
				continue
			}
			b.WriteString(l)
		}
	}
	return b.String(), nil
}

func parentDir(p string) string {
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}
