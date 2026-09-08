package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runVagrantfile(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := newVagrantfileCmd()
	cmd.SetIn(strings.NewReader(stdin))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

func TestVagrantfile_GeneratesNextToImage(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "winkit-base.qcow2")
	require.NoError(t, os.WriteFile(image, []byte("disk"), 0o644))
	cfgPath := filepath.Join(dir, "winkit.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("ports:\n  openssh: 10122\n  rdp: 13389\n"), 0o644))

	out, err := runVagrantfile(t, "", image, "-f", cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "vagrant up")

	data, err := os.ReadFile(filepath.Join(dir, "Vagrantfile"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `File.expand_path("winkit-base.qcow2", __dir__)`)
	assert.Contains(t, string(data), `qe.ssh_port = Integer(ENV.fetch("WINKIT_SSH_PORT", 10122))`)
	assert.Contains(t, string(data), `guest: 3389, host: 13389, id: "rdp", auto_correct: true`)
}

func TestVagrantfile_MissingImageErrors(t *testing.T) {
	_, err := runVagrantfile(t, "", filepath.Join(t.TempDir(), "nope.qcow2"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "winkit build")
}

func TestVagrantfile_RejectsNonQcow2(t *testing.T) {
	_, err := runVagrantfile(t, "", "image.utm")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a .qcow2")
}

func TestVagrantfile_PromptDeclinedAborts(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "winkit-base.qcow2")
	require.NoError(t, os.WriteFile(image, []byte("disk"), 0o644))
	vf := filepath.Join(dir, "Vagrantfile")
	require.NoError(t, os.WriteFile(vf, []byte("existing"), 0o644))

	_, err := runVagrantfile(t, "n\n", image)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aborted")
	data, _ := os.ReadFile(vf)
	assert.Equal(t, "existing", string(data))
}
