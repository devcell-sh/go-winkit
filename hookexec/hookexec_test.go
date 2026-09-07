package hookexec

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/buildopts"
)

type mockRunner struct {
	calls    []string
	results  []mockResult
	callIdx  int
}

type mockResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
	err      error
}

func (m *mockRunner) Run(_ context.Context, cmd string) ([]byte, []byte, int, error) {
	m.calls = append(m.calls, cmd)
	if m.callIdx < len(m.results) {
		r := m.results[m.callIdx]
		m.callIdx++
		return r.stdout, r.stderr, r.exitCode, r.err
	}
	return nil, nil, 0, nil
}

func (m *mockRunner) Close() error { return nil }

func TestInjectSpecialize_ProducesCorrectEntries(t *testing.T) {
	hooks := []buildopts.Hook{
		{Phase: buildopts.Specialize, Cmd: "reg add HKLM\\Test /v X /d 1 /f", Label: "set registry"},
		{Phase: buildopts.Specialize, Cmd: "cmd /c echo hello"},
		{Phase: buildopts.Boot, Cmd: "echo skip me"},
	}

	cmds := InjectSpecialize(hooks, 10)
	if len(cmds) != 2 {
		t.Fatalf("expected 2 specialize commands, got %d", len(cmds))
	}
	if cmds[0].Order != 10 {
		t.Fatalf("first order = %d, want 10", cmds[0].Order)
	}
	if cmds[0].Path != "reg add HKLM\\Test /v X /d 1 /f" {
		t.Fatalf("path = %q", cmds[0].Path)
	}
	if cmds[0].Description != "set registry" {
		t.Fatalf("desc = %q", cmds[0].Description)
	}
	if cmds[1].Order != 11 {
		t.Fatalf("second order = %d, want 11", cmds[1].Order)
	}
	if cmds[1].Description != "specialize hook" {
		t.Fatalf("default desc = %q", cmds[1].Description)
	}
}

func TestInjectOOBE_ProducesCorrectEntries(t *testing.T) {
	hooks := []buildopts.Hook{
		{Phase: buildopts.OOBE, Cmd: "powershell Set-ExecutionPolicy Bypass", Label: "execution policy"},
		{Phase: buildopts.Boot, Cmd: "skip"},
	}

	cmds := InjectOOBE(hooks, 5)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 oobe command, got %d", len(cmds))
	}
	if cmds[0].Order != 5 {
		t.Fatalf("order = %d, want 5", cmds[0].Order)
	}
	if cmds[0].CommandLine != "powershell Set-ExecutionPolicy Bypass" {
		t.Fatalf("cmd = %q", cmds[0].CommandLine)
	}
}

func TestInjectSpecialize_EmptyList(t *testing.T) {
	cmds := InjectSpecialize(nil, 1)
	if len(cmds) != 0 {
		t.Fatalf("expected 0 commands, got %d", len(cmds))
	}
}

func TestInjectOOBE_EmptyList(t *testing.T) {
	cmds := InjectOOBE(nil, 1)
	if len(cmds) != 0 {
		t.Fatalf("expected 0 commands, got %d", len(cmds))
	}
}

func TestExecuteSSH_RunsInOrder(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{{}, {}},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Boot, Cmd: "cmd1", Timeout: time.Minute, ValidExitCodes: []int{0}},
		{Phase: buildopts.Boot, Cmd: "cmd2", Timeout: time.Minute, ValidExitCodes: []int{0}},
	}

	results, err := ExecuteSSH(context.Background(), hooks, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d", len(results))
	}
	if len(runner.calls) != 2 || runner.calls[0] != "cmd1" || runner.calls[1] != "cmd2" {
		t.Fatalf("calls = %v", runner.calls)
	}
}

func TestExecuteSSH_SkipsNonSSHPhases(t *testing.T) {
	runner := &mockRunner{}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Specialize, Cmd: "spec"},
		{Phase: buildopts.OOBE, Cmd: "oobe"},
		{Phase: buildopts.Boot, Cmd: "boot", Timeout: time.Minute, ValidExitCodes: []int{0}},
	}

	_, err := ExecuteSSH(context.Background(), hooks, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "boot" {
		t.Fatalf("calls = %v", runner.calls)
	}
}

func TestExecuteSSH_ValidExitCodes(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{{exitCode: 3010}},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Boot, Cmd: "install.ps1", Timeout: time.Minute, ValidExitCodes: []int{0, 3010}},
	}

	results, err := ExecuteSSH(context.Background(), hooks, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].ExitCode != 3010 {
		t.Fatalf("exit code = %d", results[0].ExitCode)
	}
	if results[0].Err != nil {
		t.Fatalf("err = %v", results[0].Err)
	}
}

func TestExecuteSSH_InvalidExitCode(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{{exitCode: 1}},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Boot, Cmd: "fail.ps1", Timeout: time.Minute, ValidExitCodes: []int{0}},
	}

	_, err := ExecuteSSH(context.Background(), hooks, runner, nil)
	if err == nil {
		t.Fatal("expected error for exit code 1")
	}
}

func TestExecuteSSH_Retries(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{
			{exitCode: 1},
			{exitCode: 1},
			{exitCode: 0},
		},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Boot, Cmd: "flaky.ps1", Timeout: time.Minute, ValidExitCodes: []int{0}, Retries: 2},
	}

	var log bytes.Buffer
	results, err := ExecuteSSH(context.Background(), hooks, runner, &log)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(runner.calls))
	}
	if results[0].ExitCode != 0 {
		t.Fatalf("final exit code = %d", results[0].ExitCode)
	}
}

func TestExecuteSSH_RetriesExhausted(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{
			{exitCode: 1},
			{exitCode: 1},
		},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Boot, Cmd: "fail.ps1", Timeout: time.Minute, ValidExitCodes: []int{0}, Retries: 1},
	}

	_, err := ExecuteSSH(context.Background(), hooks, runner, nil)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
}

func TestExecuteSSH_RunnerError(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{
			{err: fmt.Errorf("connection reset")},
		},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Boot, Cmd: "cmd", Timeout: time.Minute, ValidExitCodes: []int{0}},
	}

	_, err := ExecuteSSH(context.Background(), hooks, runner, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecuteSSH_RunnerErrorWithRetries(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{
			{err: fmt.Errorf("connection reset")},
			{exitCode: 0},
		},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Boot, Cmd: "cmd", Timeout: time.Minute, ValidExitCodes: []int{0}, Retries: 1},
	}

	_, err := ExecuteSSH(context.Background(), hooks, runner, nil)
	if err != nil {
		t.Fatalf("should succeed after retry: %v", err)
	}
}

func TestExecuteSSH_Reboot(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{
			{exitCode: 0},
			{exitCode: 0}, // the reboot command
		},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Boot, Cmd: "enable-feature.ps1", Timeout: time.Minute, ValidExitCodes: []int{0}, Reboot: true},
	}

	var log bytes.Buffer
	_, err := ExecuteSSH(context.Background(), hooks, runner, &log)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected 2 calls (cmd + reboot), got %d: %v", len(runner.calls), runner.calls)
	}
	if runner.calls[1] != "shutdown /r /t 0" {
		t.Fatalf("reboot cmd = %q", runner.calls[1])
	}
}

func TestExecuteSSH_EmptyHookList(t *testing.T) {
	runner := &mockRunner{}
	results, err := ExecuteSSH(context.Background(), nil, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
	if len(runner.calls) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(runner.calls))
	}
}

func TestExecuteSSH_InternalBeforeUser(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{{}, {}, {}},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Boot, Cmd: "internal-1", Label: "internal", Timeout: time.Minute, ValidExitCodes: []int{0}},
		{Phase: buildopts.Boot, Cmd: "internal-2", Label: "internal", Timeout: time.Minute, ValidExitCodes: []int{0}},
		{Phase: buildopts.Boot, Cmd: "user-1", Label: "user", Timeout: time.Minute, ValidExitCodes: []int{0}},
	}

	_, err := ExecuteSSH(context.Background(), hooks, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if runner.calls[0] != "internal-1" || runner.calls[1] != "internal-2" || runner.calls[2] != "user-1" {
		t.Fatalf("order wrong: %v", runner.calls)
	}
}

func TestExecuteSSH_DefaultTimeoutAndExitCodes(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{{exitCode: 0}},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Boot, Cmd: "test"},
	}

	_, err := ExecuteSSH(context.Background(), hooks, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecuteSSH_DefaultExitCodeRejectsNonZero(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{{exitCode: 1}},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.Boot, Cmd: "test"},
	}

	_, err := ExecuteSSH(context.Background(), hooks, runner, nil)
	if err == nil {
		t.Fatal("expected error for exit code 1 with default valid codes")
	}
}

func TestExecuteSSH_WSLPhaseHooks(t *testing.T) {
	runner := &mockRunner{
		results: []mockResult{{exitCode: 0}},
	}
	hooks := []buildopts.Hook{
		{Phase: buildopts.WSLPhase, Cmd: "wsl -d alpine -- echo hi", Timeout: time.Minute, ValidExitCodes: []int{0}},
	}

	results, err := ExecuteSSH(context.Background(), hooks, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}
