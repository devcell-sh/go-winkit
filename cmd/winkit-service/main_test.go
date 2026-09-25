package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArgs_Install(t *testing.T) {
	args := []string{"install", "--name", "winkit-s6", "--", "wsl.exe", "-d", "winkit", "-u", "root"}
	verb, name, cmd, err := parseArgs(args)
	require.NoError(t, err)
	assert.Equal(t, "install", verb)
	assert.Equal(t, "winkit-s6", name)
	assert.Equal(t, []string{"wsl.exe", "-d", "winkit", "-u", "root"}, cmd)
}

func TestParseArgs_Stop(t *testing.T) {
	args := []string{"stop", "--name", "my-svc"}
	verb, name, cmd, err := parseArgs(args)
	require.NoError(t, err)
	assert.Equal(t, "stop", verb)
	assert.Equal(t, "my-svc", name)
	assert.Nil(t, cmd)
}

func TestParseArgs_Status(t *testing.T) {
	args := []string{"status", "--name", "my-svc"}
	verb, name, _, err := parseArgs(args)
	require.NoError(t, err)
	assert.Equal(t, "status", verb)
	assert.Equal(t, "my-svc", name)
}

func TestParseArgs_MissingVerb(t *testing.T) {
	_, _, _, err := parseArgs([]string{})
	assert.Error(t, err)
}

func TestParseArgs_MissingName(t *testing.T) {
	_, _, _, err := parseArgs([]string{"install"})
	assert.Error(t, err)
}

func TestParseArgs_InvalidVerb(t *testing.T) {
	_, _, _, err := parseArgs([]string{"destroy", "--name", "x"})
	assert.Error(t, err)
}

func TestParseArgs_InstallRequiresCommand(t *testing.T) {
	_, _, _, err := parseArgs([]string{"install", "--name", "x"})
	assert.Error(t, err)
}

func TestParseArgs_UninstallNoCommand(t *testing.T) {
	verb, name, cmd, err := parseArgs([]string{"uninstall", "--name", "x"})
	require.NoError(t, err)
	assert.Equal(t, "uninstall", verb)
	assert.Equal(t, "x", name)
	assert.Nil(t, cmd)
}

func TestParseArgs_Run(t *testing.T) {
	args := []string{"run", "--name", "test-svc", "--", "sleep", "3600"}
	verb, name, cmd, err := parseArgs(args)
	require.NoError(t, err)
	assert.Equal(t, "run", verb)
	assert.Equal(t, "test-svc", name)
	assert.Equal(t, []string{"sleep", "3600"}, cmd)
}

func TestParseArgs_RunRequiresCommand(t *testing.T) {
	_, _, _, err := parseArgs([]string{"run", "--name", "x"})
	assert.Error(t, err)
}

func TestParseArgs_RunPEAgentNoCommand(t *testing.T) {
	opts, err := parseOptions([]string{"run", "--name", "pe-agent"})
	require.NoError(t, err)
	assert.Equal(t, "run", opts.verb)
	assert.Equal(t, "pe-agent", opts.name)
	assert.Empty(t, opts.logSerial, "pe-agent auto-probes; no --log-serial needed")
	assert.Empty(t, opts.cmd)
}

func TestParseArgs_RunPEAgentExplicitPort(t *testing.T) {
	opts, err := parseOptions([]string{"run", "--name", "pe-agent", "--log-serial", `\\.\COM2`})
	require.NoError(t, err)
	assert.Equal(t, "pe-agent", opts.name)
	assert.Equal(t, `\\.\COM2`, opts.logSerial)
}

func TestParseArgs_LogVirtioAlias(t *testing.T) {
	opts, err := parseOptions([]string{"run", "--name", "pe-agent", "--log-virtio", `\\.\COM2`})
	require.NoError(t, err)
	assert.Equal(t, `\\.\COM2`, opts.logSerial, "--log-virtio must be accepted as alias for --log-serial")
}

func TestParseArgs_RunPEAgentStillAcceptsCommand(t *testing.T) {
	opts, err := parseOptions([]string{"run", "--name", "pe-agent", "--", "echo", "hello"})
	require.NoError(t, err)
	assert.Equal(t, "pe-agent", opts.name)
	assert.Equal(t, []string{"echo", "hello"}, opts.cmd)
}

func TestParseOptions_Detach(t *testing.T) {
	opts, err := parseOptions([]string{"run", "--name", "pe-agent", "--detach"})
	require.NoError(t, err)
	assert.True(t, opts.detach)
	assert.Equal(t, "pe-agent", opts.name)
}

func TestParseOptions_UserPassword(t *testing.T) {
	opts, err := parseOptions([]string{
		"install", "--name", "winkit-s6",
		"--user", `.\dmitry`, "--password", "s3cret",
		"--", "wsl.exe", "-d", "winkit",
	})
	require.NoError(t, err)
	assert.Equal(t, `.\dmitry`, opts.user)
	assert.Equal(t, "s3cret", opts.password)
	assert.Equal(t, []string{"wsl.exe", "-d", "winkit"}, opts.cmd)
}

func TestParseOptions_RunUser(t *testing.T) {
	opts, err := parseOptions([]string{
		"run-user", "--user", "winkit", "--password", "secret",
		"--current-dir", `E:\`, "--", "cmd.exe", "/c", `E:\probe.cmd`,
	})
	require.NoError(t, err)
	assert.Equal(t, "winkit", opts.user)
	assert.Equal(t, "secret", opts.password)
	assert.Equal(t, `E:\`, opts.currentDir)
	assert.Equal(t, []string{"cmd.exe", "/c", `E:\probe.cmd`}, opts.cmd)
	assert.Empty(t, opts.name)
}

func TestParseOptions_RunUserRequiresCredentialsAndCommand(t *testing.T) {
	_, err := parseOptions([]string{"run-user", "--user", "winkit", "--", "cmd.exe"})
	assert.Error(t, err)
	_, err = parseOptions([]string{"run-user", "--user", "winkit", "--password", "secret"})
	assert.Error(t, err)
}

func TestParseOptions_AddCatalogs(t *testing.T) {
	opts, err := parseOptions([]string{"add-catalogs", "--dir", `X:\Windows\WinSxS\Catalogs`})
	require.NoError(t, err)
	assert.Equal(t, "add-catalogs", opts.verb)
	assert.Equal(t, `X:\Windows\WinSxS\Catalogs`, opts.catalogDir)
	assert.Empty(t, opts.name)
}

func TestParseOptions_AddCatalogsRequiresDir(t *testing.T) {
	_, err := parseOptions([]string{"add-catalogs"})
	assert.Error(t, err)
}

func TestParseOptions_FindCatalog(t *testing.T) {
	opts, err := parseOptions([]string{"find-catalog", "--file", `X:\Windows\System32\drivers\afunix.sys`})
	require.NoError(t, err)
	assert.Equal(t, "find-catalog", opts.verb)
	assert.Equal(t, `X:\Windows\System32\drivers\afunix.sys`, opts.filePath)
}

func TestParseOptions_FindCatalogRequiresFile(t *testing.T) {
	_, err := parseOptions([]string{"find-catalog"})
	assert.Error(t, err)
}

func TestParseOptions_EnsureService(t *testing.T) {
	opts, err := parseOptions([]string{"ensure-service", "--name", "WSLService"})
	require.NoError(t, err)
	assert.Equal(t, "ensure-service", opts.verb)
	assert.Equal(t, "WSLService", opts.name)
}

func TestParseOptions_EnsureUser(t *testing.T) {
	opts, err := parseOptions([]string{"ensure-user", "--user", "winkit", "--password", "secret"})
	require.NoError(t, err)
	assert.Equal(t, "ensure-user", opts.verb)
	assert.Equal(t, "winkit", opts.user)
	assert.False(t, opts.admin)
}

func TestParseOptions_EnsureUserAdmin(t *testing.T) {
	opts, err := parseOptions([]string{"ensure-user", "--user", "winkit", "--password", "secret", "--admin"})
	require.NoError(t, err)
	assert.Equal(t, "ensure-user", opts.verb)
	assert.Equal(t, "winkit", opts.user)
	assert.True(t, opts.admin)
}

func TestParseOptions_EnsureUserRequiresCredentials(t *testing.T) {
	_, err := parseOptions([]string{"ensure-user", "--user", "winkit"})
	assert.Error(t, err)
}
