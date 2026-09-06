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
