package main

// install_authz.go — the authorization gate for installs.
//
// An install writes files into agent skill trees and host configs, so it is an
// effectful action that needs a recorded plan and a human's approval of that
// exact plan:
//
//   - the agent/bridge path (`install.execute` over IPC) never authorizes
//     itself. It must name a persisted, hash-verified, unexpired plan and
//     present an approval that a human recorded for that plan's hash
//     (`litespm approve <plan-id>` on a terminal). The approval is consumed
//     atomically, bound to the plan, and refunded if the install fails before
//     producing its effect;
//   - the human CLI path (`litespm install`) is a person typing the command: it
//     records its own plan and an approval with actor "human-cli", consumes it,
//     and runs. The approval row makes that consent auditable and replay-proof.
//
// The approval is carried to the skill installer in the context, so the policy
// "ask" for a skill write is answered only by a consumed approval, never by
// "this request exists".

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sarv-projects/litespm/internal/catalog"
	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/ipc"
	"github.com/sarv-projects/litespm/internal/policy"
	"github.com/sarv-projects/litespm/internal/state"
)

const (
	// approvalSubjectInstallPlan is the approvals.subject_type for an install plan.
	approvalSubjectInstallPlan = "install-plan"
	// approvalActorHumanCLI is the origin recorded for approvals typed by a
	// person at a terminal. The agent path accepts only this origin.
	approvalActorHumanCLI = "human-cli"
	// approvalChannelCLI is the approvals.channel for a terminal approval.
	approvalChannelCLI = "cli-tty"
	// maxHumanApprovalTTL caps an approval's lifetime.
	maxHumanApprovalTTL = 10 * time.Minute
)

type installAuthKey struct{}

// installAuthorization proves, inside this process, that an approval for the
// install in flight was consumed. It exists only in a context built by
// withInstallAuthorization after a successful ConsumeApproval.
type installAuthorization struct {
	Origin     string
	ApprovalID string
	PlanID     string
}

func withInstallAuthorization(ctx context.Context, a installAuthorization) context.Context {
	return context.WithValue(ctx, installAuthKey{}, a)
}

func installAuthorizationFrom(ctx context.Context) (installAuthorization, bool) {
	a, ok := ctx.Value(installAuthKey{}).(installAuthorization)
	return a, ok && a.ApprovalID != "" && a.PlanID != ""
}

// installGrant is a verified plan plus the consumed approval that authorizes it.
type installGrant struct {
	Plan       *domain.InstallPlan
	ApprovalID string
	Origin     string
	ListingID  string
	Version    string
	Scope      domain.InstallScope
}

func (g *installGrant) context(ctx context.Context) context.Context {
	return withInstallAuthorization(ctx, installAuthorization{
		Origin: g.Origin, ApprovalID: g.ApprovalID, PlanID: g.Plan.PlanID,
	})
}

// refund returns the approval after a failed install so the user is not made
// to re-approve a plan that produced no effect. The context is detached so a
// cancelled request still refunds.
func (g *installGrant) refund(ctx context.Context, db *state.DB) {
	_ = db.RefundApproval(context.WithoutCancel(ctx), g.ApprovalID)
}

func unauthorizedRPC(msg string) *ipc.RPCError {
	return &ipc.RPCError{Code: ipc.CodeUnauthorized, Message: msg}
}

// authorizeInstallExecute is the agent-path gate. It requires a persisted plan
// whose hash and expiry verify, request fields that agree with the plan, and a
// human-origin approval bound to that plan's hash, which it consumes.
func authorizeInstallExecute(ctx context.Context, db *state.DB, listingID, version, scope, planID, approvalID string) (*installGrant, *ipc.RPCError) {
	if strings.TrimSpace(planID) == "" {
		return nil, unauthorizedRPC("install.execute requires a recorded plan: call prepare_install first, then have a human approve the plan")
	}
	if strings.TrimSpace(approvalID) == "" {
		return nil, unauthorizedRPC(fmt.Sprintf(
			"install of plan %s requires a human approval: run `litespm approve %s` in a terminal and pass the printed approval token", planID, planID))
	}

	plan, err := db.GetPlan(ctx, planID)
	if err != nil {
		return nil, installRPCError(err)
	}
	if plan.PlanID != planID {
		return nil, installRPCError(domain.ErrPlanStale(planID, "stored plan document declares a different planId"))
	}
	computed, err := domain.ComputePlanHash(plan)
	if err != nil {
		return nil, &ipc.RPCError{Code: ipc.CodeInternalError, Message: fmt.Sprintf("failed to recompute plan hash: %v", err)}
	}
	if plan.PlanHash == "" || plan.PlanHash != computed {
		return nil, installRPCError(domain.ErrPlanStale(planID,
			fmt.Sprintf("stored planHash %q does not match recomputed %q", plan.PlanHash, computed)))
	}
	if !time.Now().UTC().Before(plan.ExpiresAt.UTC()) {
		return nil, installRPCError(domain.ErrPlanExpired(planID))
	}

	// The request may only restate what the plan already fixes.
	if listingID != "" && listingID != plan.Request.ListingID {
		return nil, installRPCError(domain.ErrStateConflict(fmt.Sprintf(
			"requested listing %q does not match plan %s listing %q", listingID, planID, plan.Request.ListingID)))
	}
	planScope := plan.Request.TargetScope
	if planScope == "" {
		planScope = domain.ScopeUser
	}
	if scope != "" && domain.InstallScope(scope) != planScope {
		return nil, installRPCError(domain.ErrStateConflict(fmt.Sprintf(
			"requested scope %q does not match plan %s target scope %q", scope, planID, planScope)))
	}
	resolved := plan.Resolved.Version
	if version != "" && version != plan.Request.RequestedVersion && version != resolved {
		return nil, installRPCError(domain.ErrStateConflict(fmt.Sprintf(
			"requested version %q matches neither plan %s requested version %q nor resolved version %q",
			version, planID, plan.Request.RequestedVersion, resolved)))
	}
	if resolved == "" {
		resolved = plan.Request.RequestedVersion
	}

	// Only an approval a human recorded on a terminal opens this gate.
	rec, err := db.GetApproval(ctx, approvalID)
	if err != nil {
		return nil, unauthorizedRPC(fmt.Sprintf("approval %s is not valid: %v", approvalID, err))
	}
	if rec.Actor != approvalActorHumanCLI || rec.Channel != approvalChannelCLI {
		return nil, unauthorizedRPC(fmt.Sprintf("approval %s was not recorded by a human at a terminal", approvalID))
	}
	if err := db.ConsumeApproval(ctx, approvalID, approvalSubjectInstallPlan, plan.PlanHash); err != nil {
		return nil, unauthorizedRPC(err.Error())
	}

	return &installGrant{
		Plan: plan, ApprovalID: approvalID, Origin: rec.Actor,
		ListingID: plan.Request.ListingID, Version: resolved, Scope: planScope,
	}, nil
}

// newApprovalID returns a random, unguessable approval token.
func newApprovalID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("failed to generate approval id: %w", err)
	}
	return "appr_" + hex.EncodeToString(buf[:]), nil
}

// recordHumanApproval records an active approval for plan with origin
// human-cli, expiring with the plan (capped).
func recordHumanApproval(ctx context.Context, db *state.DB, plan *domain.InstallPlan) (string, error) {
	id, err := newApprovalID()
	if err != nil {
		return "", err
	}
	expires := time.Now().UTC().Add(maxHumanApprovalTTL)
	if pe := plan.ExpiresAt.UTC(); pe.Before(expires) {
		expires = pe
	}
	scope := string(plan.Request.TargetScope)
	if scope == "" {
		scope = string(domain.ScopeUser)
	}
	if err := db.RecordApproval(ctx, id, approvalSubjectInstallPlan, plan.PlanHash,
		approvalActorHumanCLI, approvalChannelCLI, scope, &expires); err != nil {
		return "", err
	}
	return id, nil
}

// authorizeHumanInstall is the `litespm install` gate. The person ran the
// command, so the consent is theirs; it is still recorded as a plan plus a
// human-cli approval, consumed before any write, so every install has the same
// auditable shape as an agent-path install.
func authorizeHumanInstall(ctx context.Context, db *state.DB, catClient *catalog.Client, listing *domain.Listing, version string, scope domain.InstallScope) (*installGrant, error) {
	plan, rpcErr := buildInstallPlan(ctx, catClient, listing.ID, version, scope, listing)
	if rpcErr != nil {
		return nil, fmt.Errorf("plan install: %s", rpcErr.Message)
	}
	if err := db.SavePlan(ctx, plan); err != nil {
		return nil, fmt.Errorf("record plan %s: %w", plan.PlanID, err)
	}
	approvalID, err := recordHumanApproval(ctx, db, plan)
	if err != nil {
		return nil, err
	}
	if err := db.ConsumeApproval(ctx, approvalID, approvalSubjectInstallPlan, plan.PlanHash); err != nil {
		return nil, err
	}
	resolved := plan.Resolved.Version
	if resolved == "" {
		resolved = version
	}
	return &installGrant{
		Plan: plan, ApprovalID: approvalID, Origin: approvalActorHumanCLI,
		ListingID: plan.Request.ListingID, Version: resolved, Scope: plan.Request.TargetScope,
	}, nil
}

// newPolicyEngine builds the policy engine with its real tiers: capability
// grants from the state database and the user's deny rules from the data root.
// A deny-rules file that exists but cannot be read fails closed.
func newPolicyEngine(db *state.DB, dataRoot string) (*policy.Engine, error) {
	rules, err := policy.LoadDenyRules(dataRoot + string(os.PathSeparator) + policy.DenyRulesFile)
	if err != nil {
		return nil, err
	}
	return policy.NewEngine(db, rules), nil
}

// openCLIPolicyEngine is newPolicyEngine for CLI commands that do not already
// hold a state database. The returned close func releases the database.
func openCLIPolicyEngine(dataRoot string) (*policy.Engine, func(), error) {
	paths := &config.PlatformPaths{DataRoot: dataRoot}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		return nil, func() {}, fmt.Errorf("open state database for the policy engine: %w", err)
	}
	eng, err := newPolicyEngine(db, dataRoot)
	if err != nil {
		_ = db.Close()
		return nil, func() {}, err
	}
	return eng, func() { _ = db.Close() }, nil
}

// runApprove implements `litespm approve <plan-id>`: a person reviews a stored
// install plan on a terminal and, by typing "yes", records an approval for it
// and receives the token an agent passes to request_install. Refuses to run
// without an interactive terminal so an agent cannot script it.
func runApprove(args []string) {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "Usage: litespm approve <plan-id>")
		os.Exit(2)
	}
	planID := args[0]
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		fmt.Fprintln(os.Stderr, "litespm approve requires an interactive terminal: approvals are given by a person, not a script.")
		os.Exit(1)
	}

	paths, err := config.ResolvePlatformPaths()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to resolve platform paths: %v\n", err)
		os.Exit(1)
	}
	if err := paths.EnsureDirectories(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to initialize storage directories: %v\n", err)
		os.Exit(1)
	}
	db, err := state.Open(paths.StateDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: failed to open state database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx := context.Background()
	plan, err := db.GetPlan(ctx, planID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Approve failed: %v\n", err)
		os.Exit(1)
	}
	computed, herr := domain.ComputePlanHash(plan)
	if herr != nil || plan.PlanHash == "" || computed != plan.PlanHash {
		fmt.Fprintf(os.Stderr, "Approve failed: plan %s does not match its recorded hash\n", planID)
		os.Exit(1)
	}
	if !time.Now().UTC().Before(plan.ExpiresAt.UTC()) {
		fmt.Fprintf(os.Stderr, "Approve failed: %v\n", domain.ErrPlanExpired(planID))
		os.Exit(1)
	}

	fmt.Printf("Plan %s\n", plan.PlanID)
	fmt.Printf("  listing : %s\n", plan.Request.ListingID)
	fmt.Printf("  version : %s\n", firstNonEmpty(plan.Resolved.Version, plan.Request.RequestedVersion, "latest"))
	fmt.Printf("  scope   : %s\n", plan.Request.TargetScope)
	fmt.Printf("  effects : %s\n", strings.Join(plan.Effects, ", "))
	fmt.Printf("  expires : %s\n", plan.ExpiresAt.UTC().Format(time.RFC3339))
	fmt.Print("Type 'yes' to approve this install: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "yes" {
		fmt.Println("Not approved.")
		os.Exit(1)
	}

	id, err := recordHumanApproval(ctx, db, plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Approve failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Approved. Approval token (single use, expires in at most %d minutes):\n%s\n", int(maxHumanApprovalTTL.Minutes()), id)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
