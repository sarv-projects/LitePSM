package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeLookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestBuildChildEnv_MinimalBaseOnly(t *testing.T) {
	parent := map[string]string{
		"PATH": "/usr/bin", "HOME": "/home/u", "LANG": "C", "TMPDIR": "/tmp",
		"AWS_SECRET_ACCESS_KEY": "canary", "GITHUB_TOKEN": "canary",
	}
	env := buildChildEnv(fakeLookup(parent), false, nil, nil)
	joined := strings.Join(env, "\n")
	for _, bad := range []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN", "canary"} {
		if strings.Contains(joined, bad) {
			t.Errorf("env leaked %q: %v", bad, env)
		}
	}
	for _, want := range []string{"PATH=/usr/bin", "HOME=/home/u", "LANG=C", "TMPDIR=/tmp"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing base %q in %v", want, env)
		}
	}
}

func TestBuildChildEnv_LayeringAndDedupe(t *testing.T) {
	parent := map[string]string{"PATH": "/bin", "HOME": "/h"}
	env := buildChildEnv(fakeLookup(parent), false,
		map[string]string{"HOME": "/spec", "X": "1"},
		map[string]string{"X": "secret", "TOKEN": "t"})
	want := []string{"HOME=/spec", "PATH=/bin", "TOKEN=t", "X=secret"}
	if fmt.Sprint(env) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", env, want)
	}
	// Deterministic across runs.
	for i := 0; i < 20; i++ {
		if again := buildChildEnv(fakeLookup(parent), false,
			map[string]string{"HOME": "/spec", "X": "1"},
			map[string]string{"X": "secret", "TOKEN": "t"}); fmt.Sprint(again) != fmt.Sprint(want) {
			t.Fatalf("non-deterministic: %v", again)
		}
	}
}

func TestBuildChildEnv_WindowsCaseFoldAndSystemRoot(t *testing.T) {
	parent := map[string]string{"Path": "x", "SystemRoot": `C:\Windows`, "PATH": "C:\\bin", "SECRET": "no"}
	env := buildChildEnv(fakeLookup(parent), true,
		map[string]string{"path": "C:\\spec"}, map[string]string{"Token": "s"})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, `SystemRoot=C:\Windows`) {
		t.Errorf("SystemRoot missing: %v", env)
	}
	if strings.Contains(joined, "SECRET") {
		t.Errorf("leaked: %v", env)
	}
	n := 0
	for _, e := range env {
		if strings.EqualFold(strings.SplitN(e, "=", 2)[0], "PATH") {
			n++
			if !strings.HasSuffix(e, `C:\spec`) {
				t.Errorf("last layer must win, got %s", e)
			}
		}
	}
	if n != 1 {
		t.Errorf("PATH duplicated %d times: %v", n, env)
	}
}

func TestBuildChildEnv_RejectsMalformedEntries(t *testing.T) {
	env := buildChildEnv(fakeLookup(nil), false,
		map[string]string{"A=B": "v", "": "v", "OK": "va\x00l"}, map[string]string{"GOOD": "1"})
	if fmt.Sprint(env) != "[GOOD=1]" {
		t.Errorf("got %v", env)
	}
}

func dumpEnvCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/c", "set"}
	}
	return "/bin/sh", []string{"-c", "env"}
}

// A canary in the parent environment must never reach the child, while the
// spec env and the resolved secret env do.
func TestSupervisor_ParentEnvCanaryNeverReachesChild(t *testing.T) {
	t.Setenv("LITESPM_CANARY_PARENT", "canary-parent-value")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exe, args := dumpEnvCommand()
	sup := NewSupervisor()
	h, err := sup.StartProvider(ctx, "env-canary", LaunchSpec{
		Executable: exe, Args: args, WorkingDir: os.TempDir(),
		Env:       map[string]string{"LITESPM_FROM_SPEC": "spec-value"},
		SecretEnv: map[string]string{"LITESPM_FROM_SECRET": "secret-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(h.Stdout)
	_ = h.Terminate(time.Second)
	got := string(out)
	if strings.Contains(got, "LITESPM_CANARY_PARENT") || strings.Contains(got, "canary-parent-value") {
		t.Fatalf("parent canary leaked into child env:\n%s", got)
	}
	for _, want := range []string{"LITESPM_FROM_SPEC=spec-value", "LITESPM_FROM_SECRET=secret-value"} {
		if !strings.Contains(got, want) {
			t.Errorf("child env missing %q:\n%s", want, got)
		}
	}
}

// E3: an isolation failure kills the process and returns a typed error; no
// provider is registered.
func TestSupervisor_IsolationFailureKillsProcessAndFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a unix sleep fixture")
	}
	orig := postStartIsolation
	defer func() { postStartIsolation = orig }()
	injected := errors.New("injected isolation failure")
	var pid int
	postStartIsolation = func(cmd *exec.Cmd, h *ProviderHandle) error {
		pid = cmd.Process.Pid
		return injected
	}

	sup := NewSupervisor()
	h, err := sup.StartProvider(context.Background(), "iso-fail", LaunchSpec{
		Executable: "/bin/sh", Args: []string{"-c", "sleep 30"}, WorkingDir: os.TempDir(),
	})
	if err == nil || h != nil {
		t.Fatalf("expected failure, got handle=%v err=%v", h, err)
	}
	var iso *IsolationError
	if !errors.As(err, &iso) || iso.ProviderID != "iso-fail" || !errors.Is(err, injected) {
		t.Fatalf("expected typed *IsolationError wrapping the cause, got %T %v", err, err)
	}
	if _, gerr := sup.GetProvider("iso-fail"); gerr == nil {
		t.Error("failed provider must not be registered")
	}
	if pid <= 0 {
		t.Fatal("hook did not run")
	}
	if processAlive(pid) {
		t.Errorf("process %d still alive after isolation failure", pid)
	}
}
