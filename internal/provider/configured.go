package provider

import (
	"context"
	"fmt"
)

// Launch modes persisted in the providers table (schema CHECK constraint).
const (
	ModeLocalStdio = "local-stdio"
	ModeRemoteHTTP = "remote-http"
	ModeLegacySSE  = "legacy-sse"
)

// ConfiguredProvider is one persisted provider row translated for startup
// decisions. Spec is nil when the stored launch_spec_json could not be parsed;
// SpecErr then carries the real decoding error.
type ConfiguredProvider struct {
	ProviderID string
	Mode       string
	Enabled    bool
	Autostart  bool
	Spec       *LaunchSpec
	SpecErr    string
}

// StartReport states exactly what happened to one configured provider.
// Action is one of "started", "skipped", or "failed"; nothing else is ever
// reported, so a provider is never described as running unless the supervisor
// actually spawned it.
type StartReport struct {
	ProviderID string
	Action     string
	Detail     string
}

// StartConfigured starts every enabled, autostart, local-stdio provider whose
// launch spec parses. Disabled rows, autostart-off rows, remote modes, and
// unparseable launch specs are reported with their reason instead of being
// silently ignored.
func StartConfigured(ctx context.Context, s *Supervisor, entries []ConfiguredProvider) []StartReport {
	reports := make([]StartReport, 0, len(entries))
	for _, e := range entries {
		switch {
		case !e.Enabled:
			reports = append(reports, StartReport{e.ProviderID, "skipped", "provider is disabled in the state database"})
		case !e.Autostart:
			reports = append(reports, StartReport{e.ProviderID, "skipped", "autostart is not enabled for this provider"})
		case e.Mode != ModeLocalStdio:
			reports = append(reports, StartReport{e.ProviderID, "skipped",
				fmt.Sprintf("mode %q is not launched by the process supervisor", e.Mode)})
		case e.SpecErr != "":
			reports = append(reports, StartReport{e.ProviderID, "failed",
				fmt.Sprintf("stored launch spec is invalid: %s", e.SpecErr)})
		case e.Spec == nil || e.Spec.Executable == "":
			reports = append(reports, StartReport{e.ProviderID, "failed", "launch spec declares no executable"})
		default:
			if _, err := s.StartProvider(ctx, e.ProviderID, *e.Spec); err != nil {
				reports = append(reports, StartReport{e.ProviderID, "failed", err.Error()})
				continue
			}
			reports = append(reports, StartReport{e.ProviderID, "started",
				fmt.Sprintf("spawned pid supervision for executable %q", e.Spec.Executable)})
		}
	}
	return reports
}
