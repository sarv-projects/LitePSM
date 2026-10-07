package main

// grant.go — `litespm grant`: the explicit, human-given authorization that
// turns a policy "ask" into an active CapabilityGrant (ARCH/15 §4).
//
// Every tool invocation is classified — never guessed (ARCH/15 §1.1) — as
// provider.start + process.spawn, which the policy engine answers with "ask"
// until a grant bound to the capability's schema fingerprint exists. Without
// the engine the daemon's invoke path fails closed on everything ("no policy
// engine configured"); with the engine attached it fails closed on
// *unapproved* invocations and says so. This command is the "output CLI
// command" half of ARCH/15 §3: a person on an interactive terminal reviews
// what they are authorizing and types "yes". The same terminal gate as
// `litespm approve` — approvals are given by people, not scripts.
//
// One grant row per capability (grantIDFor is deterministic), so a
// re-approval after a schema change REBINDS the row instead of leaving a
// stale active row that the engine's drift check would keep denying.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/state"
)

// grantEffects are the effects discover.Invoke declares for an arbitrary
// stdio tool: LiteSPM does not guess what the tool does, only that it starts
// a provider process under the user's account.
var grantEffects = []string{"provider.start", "process.spawn"}

// grantIDFor derives the single grant row for a capability. Deterministic so
// re-approval updates the existing authorization rather than accumulating
// rows (two active rows with different fingerprints would make the engine's
// drift check deny forever).
func grantIDFor(capabilityID string) string {
	sum := sha256.Sum256([]byte(capabilityID))
	return "grant_" + hex.EncodeToString(sum[:])[:24]
}

// providerDisplay renders a provider record's command line for humans.
func providerDisplay(p *domain.ProviderRecord) string {
	line := strings.TrimSpace(p.Command)
	if p.ArgsJSON != "" && p.ArgsJSON != "[]" {
		var args []string
		if json.Unmarshal([]byte(p.ArgsJSON), &args) == nil && len(args) > 0 {
			line = strings.TrimSpace(line + " " + strings.Join(args, " "))
		}
	}
	if line == "" {
		return "(provider not resolved)"
	}
	return line
}

// issueCapabilityGrant records the authorization bound to the capability's
// CURRENT schema fingerprint. An unknown capability or one without a
// fingerprint is refused: a grant must bind to something verifiable.
func issueCapabilityGrant(ctx context.Context, db *state.DB, capabilityID string) (*domain.CapabilityGrant, error) {
	if db == nil {
		return nil, fmt.Errorf("grant: no state database")
	}
	capRec, err := db.GetCapability(ctx, capabilityID)
	if err != nil {
		return nil, fmt.Errorf("unknown capability %q (run discovery or `litespm search` first): %w", capabilityID, err)
	}
	if strings.TrimSpace(capRec.SchemaFingerprint) == "" {
		return nil, fmt.Errorf("capability %q has no schema fingerprint; refusing an unbindable grant", capabilityID)
	}

	g := &domain.CapabilityGrant{
		GrantID:           grantIDFor(capabilityID),
		CapabilityID:      capabilityID,
		SchemaFingerprint: capRec.SchemaFingerprint,
		Status:            "active",
		CreatedAt:         time.Now().UTC(),
	}
	if err := db.SaveCapabilityGrant(ctx, g, approvalActorHumanCLI); err != nil {
		return nil, fmt.Errorf("record grant: %w", err)
	}
	g.GrantedBy = approvalActorHumanCLI
	return g, nil
}

// runGrant is the CLI entry point.
//
//	grant <capabilityId>        issue or re-approve (interactive)
//	grant list                  show recorded grants
//	grant revoke <capabilityId> revoke a grant
func runGrant(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: litespm grant <capabilityId> | `litespm grant list` | litespm grant revoke <capabilityId>")
		os.Exit(2)
	}

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving paths: %v\n", err)
		os.Exit(1)
	}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening state: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	switch {
	case args[0] == "list" && len(args) == 1:
		grantList(ctx, db)
		return
	case args[0] == "revoke" && len(args) == 2:
		grantRevoke(ctx, db, args[1])
		return
	case len(args) == 1 && !strings.HasPrefix(args[0], "-"):
		grantIssue(ctx, db, args[0])
		return
	default:
		fmt.Fprintln(os.Stderr, "Usage: litespm grant <capabilityId> | litespm grant list | litespm grant revoke <capabilityId>")
		os.Exit(2)
	}
}

// grantIssue shows the authorization and takes the human "yes".
func grantIssue(ctx context.Context, db *state.DB, capabilityID string) {
	// Refuse to run without an interactive terminal so an agent cannot
	// script its own authorization.
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		fmt.Fprintln(os.Stderr, "litespm grant requires an interactive terminal: grants are given by a person, not a script.")
		os.Exit(1)
	}

	// Resolve and display BEFORE prompting; on an unknown capability we exit
	// before asking anything.
	capRec, err := db.GetCapability(ctx, capabilityID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Grant failed: unknown capability %q (run discovery or `litespm search` first)\n", capabilityID)
		os.Exit(1)
	}
	providerLine := "(provider not resolved)"
	if p, perr := db.GetProvider(ctx, capRec.ProviderID); perr == nil && p != nil {
		providerLine = providerDisplay(p)
	}

	fmt.Printf("Capability %s\n", capabilityID)
	fmt.Printf("  title   : %s\n", firstNonEmpty(capRec.Name, capRec.Description))
	fmt.Printf("  provider: %s\n", providerLine)
	fmt.Printf("  schema  : %s\n", capRec.SchemaFingerprint)
	fmt.Printf("  effects : %s\n", strings.Join(grantEffects, ", "))
	fmt.Printf("  grants  : agents may invoke this tool until the grant is revoked\n")
	fmt.Print("Type 'yes' to grant this capability: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "yes" {
		fmt.Println("Not granted.")
		os.Exit(1)
	}

	g, err := issueCapabilityGrant(ctx, db, capabilityID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Grant failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Granted. Grant id: %s (schema %s)\n", g.GrantID, g.SchemaFingerprint)
	fmt.Println("  Invocations now pass the policy gate; `litespm grant revoke` takes it back.")
}

// grantList prints the recorded grants with honest statuses.
func grantList(ctx context.Context, db *state.DB) {
	grants, err := db.ListCapabilityGrants(ctx, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "List grants failed: %v\n", err)
		os.Exit(1)
	}
	if len(grants) == 0 {
		fmt.Println("No capability grants recorded. `litespm grant <capabilityId>` creates one after an interactive confirmation.")
		return
	}
	for _, g := range grants {
		expires := "no expiry"
		if g.ExpiresAt != nil {
			expires = "expires " + g.ExpiresAt.UTC().Format(time.RFC3339)
		}
		fmt.Printf("%-14s %-64s %s  %s  granted %s by %s\n",
			g.Status, g.CapabilityID, shortFingerprint(g.SchemaFingerprint), expires,
			g.CreatedAt.UTC().Format(time.RFC3339), firstNonEmpty(g.GrantedBy, "unknown"))
	}
}

// grantRevoke revokes a grant; revoking something that was never granted is
// an honest error, not a no-op success.
func grantRevoke(ctx context.Context, db *state.DB, capabilityID string) {
	grants, err := db.ListCapabilityGrants(ctx, capabilityID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Revoke failed: %v\n", err)
		os.Exit(1)
	}
	for _, g := range grants {
		if g.Status == "revoked" {
			continue
		}
		if err := db.SetCapabilityGrantStatus(ctx, g.GrantID, "revoked"); err != nil {
			fmt.Fprintf(os.Stderr, "Revoke failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Revoked %s for %s\n", g.GrantID, g.CapabilityID)
	}
	if len(grants) == 0 {
		fmt.Fprintf(os.Stderr, "Revoke failed: no grant recorded for %s\n", capabilityID)
		os.Exit(1)
	}
}

// shortFingerprint renders a grant fingerprint for humans (full value on very
// short inputs).
func shortFingerprint(fp string) string {
	if len(fp) <= 20 {
		return fp
	}
	return fp[:20] + "…"
}
