package main

// skills_policy.go — the bridge between the CLI's skill lifecycle commands and
// the canonical security policy engine.
//
// The skills package deliberately takes an injected PolicyChecker function so
// its leaf layer stays free of the policy/state dependency chain. This file is
// the adapter: it maps a skills.PolicyRequest onto a policy.PolicyInput, runs
// the engine, and maps the engine verdict back.
//
// Honesty rules:
//   - an engine "allow" is the only verdict that proceeds without further
//     action;
//   - an engine "ask" is never turned into an approval by this adapter. The
//     caller supplies an ask resolver, and a nil resolver fails the ask closed;
//   - an engine "deny" (and the engine's fail-closed default) is always a
//     denial.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sarv-projects/litespm/internal/domain"
	"github.com/sarv-projects/litespm/internal/policy"
	"github.com/sarv-projects/litespm/internal/skills"
)

// skillPolicyAsker resolves an engine "ask" into an explicit decision. It is
// called only for "ask"; allow/deny are handled by the engine.
type skillPolicyAsker func(ctx context.Context, req skills.PolicyRequest, detail string) bool

// skillPolicyInput maps a skill write onto the canonical policy input. The
// capability identity fields are intentionally empty: a skill directory is not
// a provider capability, so tier-3 grant matching does not apply.
func skillPolicyInput(req skills.PolicyRequest) policy.PolicyInput {
	effects := make([]policy.EffectDeclaration, 0, len(req.Effects))
	for _, e := range req.Effects {
		effects = append(effects, policy.EffectDeclaration{
			Effect:     policy.CanonicalEffect(e),
			Provenance: domain.ProvenanceUserClassified,
			Target:     req.DestDir,
		})
	}
	scope := domain.ScopeProject
	if req.Scope == "global" {
		scope = domain.ScopeUser
	}
	return policy.PolicyInput{
		Actor:     "user",
		Operation: req.Operation,
		TargetRef: req.SkillName,
		Effects:   effects,
		Scope:     scope,
	}
}

// enginePolicyChecker adapts a policy engine to skills.PolicyChecker. It
// returns nil when the engine is nil so a missing wiring is explicit rather
// than silently permissive.
func enginePolicyChecker(engine *policy.Engine, ask skillPolicyAsker) skills.PolicyChecker {
	if engine == nil {
		return nil
	}
	return func(ctx context.Context, req skills.PolicyRequest) skills.PolicyVerdict {
		decision := engine.Evaluate(ctx, skillPolicyInput(req))
		verdict := skills.PolicyVerdict{
			Decision: string(decision.Decision),
			Reason:   decision.Detail,
			Reasons:  decision.ReasonCodes,
		}
		switch decision.Decision {
		case policy.DecisionAllow:
			verdict.Allowed = true
		case policy.DecisionAsk:
			if ask != nil && ask(ctx, req, decision.Detail) {
				verdict.Allowed = true
				if strings.TrimSpace(verdict.Reason) == "" {
					verdict.Reason = "approved after policy consult"
				}
			}
		default:
			// deny and every non-allow, non-ask value stay denied.
		}
		return verdict
	}
}

// interactivePolicyAsker resolves policy asks for `skills update`. Unlike
// `skills add`, the update command has no pre-existing consent gate, so an ask
// is resolved here: --yes is an explicit non-interactive approval; otherwise a
// terminal prompts once and caches the answer. With no terminal and no --yes
// the ask fails closed.
type interactivePolicyAsker struct {
	interactive bool
	yes         bool
	asked       bool
	approved    bool
}

func newInteractivePolicyAsker(yes bool) *interactivePolicyAsker {
	return &interactivePolicyAsker{
		interactive: isTerminal(os.Stdin),
		yes:         yes,
	}
}

func (a *interactivePolicyAsker) resolve(_ context.Context, req skills.PolicyRequest, detail string) bool {
	if a.yes {
		return true
	}
	if !a.interactive {
		fmt.Fprintf(os.Stderr, "policy: %s %s requires interactive approval (%s); re-run with --yes to authorize non-interactively\n",
			req.Operation, req.SkillName, detail)
		return false
	}
	if a.asked {
		return a.approved
	}
	a.asked = true
	fmt.Fprintf(os.Stderr, "Policy requires approval: %s %s — %s [y/N] ", req.Operation, req.SkillName, detail)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	a.approved = isAffirmative(line)
	return a.approved
}

// isAffirmative reports whether an interactive answer is a yes.
func isAffirmative(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
