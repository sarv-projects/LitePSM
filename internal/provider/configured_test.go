package provider

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

// sleepCommand returns a long-running command available on the current OS.
func sleepCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/c", "ping -n 30 127.0.0.1 >NUL"}
	}
	return "/bin/sh", []string{"-c", "sleep 30"}
}

func TestStartConfigured_ReportsEveryOutcome(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sup := NewSupervisor()
	defer sup.StopAll(ctx)

	exe, args := sleepCommand()
	entries := []ConfiguredProvider{
		{
			ProviderID: "prov_started",
			Mode:       ModeLocalStdio,
			Enabled:    true,
			Autostart:  true,
			Spec:       &LaunchSpec{Executable: exe, Args: args, WorkingDir: os.TempDir()},
		},
		{ProviderID: "prov_disabled", Mode: ModeLocalStdio, Enabled: false, Autostart: true,
			Spec: &LaunchSpec{Executable: exe, Args: args}},
		{ProviderID: "prov_no_autostart", Mode: ModeLocalStdio, Enabled: true, Autostart: false,
			Spec: &LaunchSpec{Executable: exe, Args: args}},
		{ProviderID: "prov_remote", Mode: ModeRemoteHTTP, Enabled: true, Autostart: true,
			Spec: &LaunchSpec{Executable: exe, Args: args}},
		{ProviderID: "prov_bad_spec", Mode: ModeLocalStdio, Enabled: true, Autostart: true,
			SpecErr: "invalid character 'x' looking for beginning of value"},
		{ProviderID: "prov_no_exec", Mode: ModeLocalStdio, Enabled: true, Autostart: true,
			Spec: &LaunchSpec{}},
	}

	reports := StartConfigured(ctx, sup, entries)
	if len(reports) != len(entries) {
		t.Fatalf("expected %d reports, got %d", len(entries), len(reports))
	}

	wantActions := map[string]string{
		"prov_started":      "started",
		"prov_disabled":     "skipped",
		"prov_no_autostart": "skipped",
		"prov_remote":       "skipped",
		"prov_bad_spec":     "failed",
		"prov_no_exec":      "failed",
	}
	for _, r := range reports {
		want := wantActions[r.ProviderID]
		if r.Action != want {
			t.Errorf("%s: expected action %q, got %q (%s)", r.ProviderID, want, r.Action, r.Detail)
		}
		if r.Detail == "" {
			t.Errorf("%s: report must explain the outcome", r.ProviderID)
		}
	}

	// Only the started provider may be tracked.
	if _, err := sup.GetProvider("prov_started"); err != nil {
		t.Errorf("expected prov_started to be supervised: %v", err)
	}
	for _, id := range []string{"prov_disabled", "prov_no_autostart", "prov_remote", "prov_bad_spec", "prov_no_exec"} {
		if _, err := sup.GetProvider(id); err == nil {
			t.Errorf("%s must not be tracked by the supervisor", id)
		}
	}
}

func TestSnapshotProvider_RealProcessFacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	sup := NewSupervisor()
	defer sup.StopAll(ctx)

	exe, args := sleepCommand()
	handle, err := sup.StartProvider(ctx, "prov_snap", LaunchSpec{Executable: exe, Args: args, WorkingDir: os.TempDir()})
	if err != nil {
		t.Fatalf("StartProvider failed: %v", err)
	}

	snap, err := sup.SnapshotProvider("prov_snap")
	if err != nil {
		t.Fatalf("SnapshotProvider failed: %v", err)
	}
	if snap.ProviderID != "prov_snap" || snap.PID != handle.PID || snap.Status != StatusRunning {
		t.Errorf("unexpected snapshot: %+v", snap)
	}
	if snap.StartedAt.IsZero() {
		t.Error("snapshot must carry the real start time")
	}

	if _, err := sup.SnapshotProvider("prov_missing"); err == nil {
		t.Error("expected not-found for an untracked provider")
	}
}
