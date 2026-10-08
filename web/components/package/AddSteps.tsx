"use client";

import React from "react";
import { Check } from "lucide-react";
import type { Listing } from "../../lib/telemetry";
import type { AgentChoice } from "../../lib/landing";
import { CopyButton } from "./CopyButton";
import { ManualSetup } from "./ManualSetup";

const INSTALL_COMMAND_PREFIX = "litespm install ";

/**
 * Scroll a section into view and hand it focus, without a `#hash` link —
 * the detail view only exists after `?slug=` resolves in the browser, so a
 * fragment href would point at ids the prerendered HTML never contains.
 * Reduced-motion readers get an instant cut, per ARCH/25 §5.3.
 */
/** Smooth-in, instant-under-reduced-motion, then hand the section focus. */
export function scrollToSection(id: string): void {
  const el = document.getElementById(id);
  if (!el) return;
  const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  el.scrollIntoView({ behavior: reduce ? "auto" : "smooth", block: "start" });
  el.focus({ preventScroll: true });
}

/** A command line on the dark surface: monospaced, selectable, copyable. */
function DarkCmd({ text, label, message }: { text: string; label: string; message: string }) {
  return (
    <div className="mt-2 flex items-center gap-2 border border-dark-rule bg-dark-2 px-3 py-2.5">
      <span className="t-mono shrink-0 text-[12px] text-dark-ink-2" aria-hidden="true">
        $
      </span>
      <code className="t-mono min-w-0 flex-1 break-all text-[12px] text-dark-ink select-all">{text}</code>
      <CopyButton text={text} label={label} message={message} />
    </div>
  );
}

export interface AddStepsProps {
  item: Listing;
  /** The agent the reader picked (`litespm-agent`); null when unset. */
  agent: AgentChoice | null;
  /** Scrolls to the "Will it work with what I use?" selector. */
  onChooseAgent: () => void;
}

/**
 * Question five of the package page — "How do I add it?" — and the only place
 * on this route that tells a reader to run something.
 *
 * ARCH/25 §7.3 shapes it as a state that reflects the selected agent plus a
 * three-step guided flow, because a static page cannot detect whether LiteSPM
 * is installed in a browser. The two states therefore speak about the one
 * signal that actually exists — the agent choice stored in this browser — and
 * never about a local installation:
 *
 *   installable + selected  → "You chose <agent>" + a primary action that
 *                             takes you to the install command;
 *   installable + unset     → "No agent selected yet" + [Choose your agent];
 *   otherwise               → the CLI's fail-closed status, without an install
 *                             command for an entry it cannot install.
 *
 * The generic host-setup block (agent × OS config path + raw snippet) lives in
 * the single `ManualSetup` disclosure below the panel — never above the fold,
 * and nowhere else on the page.
 */
export function AddSteps({ item, agent, onChooseAgent }: AddStepsProps) {
  // The CLI treats an unset installability value as discovery-only. Plugins
  // also fail closed today because their artifact source is not wired.
  const installabilityEstablished =
    item.installability === "metadata_verified" ||
    item.installability === "runtime_verified" ||
    item.installability === "litespm_tested";
  const liteSPMInstallable = item.kind !== "plugin" && installabilityEstablished;
  const targeted = liteSPMInstallable && agent !== null && agent.hosts.length > 0;

  return (
    <section id="pkg-add" aria-labelledby="pkg-add-title" tabIndex={-1} className="scroll-mt-16">
      <div className="section-head">
        <h2 id="pkg-add-title">How do I add it?</h2>
        <span className="section-note">
          {liteSPMInstallable ? "three steps, in order" : "installability status"}
        </span>
      </div>

      <div className="on-dark panel-dark mt-3">
        {/* ---- state: what this page can actually know ---- */}
        {!liteSPMInstallable ? (
          <p className="border-b border-dark-rule pb-3.5 text-[13px] leading-relaxed text-dark-ink">
            {item.kind === "plugin" && installabilityEstablished ? (
              <>
                LiteSPM cannot complete plugin installs yet and fails closed with{" "}
                <code className="t-mono text-dark-ink">LPSM-ARTIFACT-UNAVAILABLE</code>.
              </>
            ) : (
              <>
                The catalog has not established that this entry is installable. The CLI refuses
                discovery-only or unclassified entries with{" "}
                <code className="t-mono text-dark-ink">LPSM-NOT-INSTALLABLE</code>; no LiteSPM install
                command is shown.
              </>
            )}
          </p>
        ) : targeted ? (
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-dark-rule pb-3.5">
            <p className="flex min-w-0 items-start gap-2 text-[13px] leading-relaxed text-dark-ink">
              <Check className="mt-0.5 h-4 w-4 shrink-0 text-dark-signal" aria-hidden="true" />
              <span>
                You chose <strong className="font-medium">{agent?.label}</strong> — your pick, stored
                in this browser only.
              </span>
            </p>
            <button
              type="button"
              className="btn btn-solid btn-lg shrink-0"
              onClick={() => scrollToSection("pkg-step-add")}
            >
              Add {item.name} to {agent?.label}
            </button>
          </div>
        ) : (
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-dark-rule pb-3.5">
            <p className="min-w-0 text-[13px] leading-relaxed text-dark-ink">
              {agent ? (
                <>
                  You chose <strong className="font-medium text-dark-ink">&ldquo;{agent.label}&rdquo;</strong>,
                  which doesn&rsquo;t name an install target — so the steps below stay general.
                </>
              ) : (
                <>No agent selected yet. Pick one and these steps target it; until then they work for any agent.</>
              )}
            </p>
            <button type="button" className="btn btn-solid btn-lg shrink-0" onClick={onChooseAgent}>
              Choose your agent
            </button>
          </div>
        )}

        {/* ---- the guided flow is shown only for entries the CLI can install ---- */}
        {liteSPMInstallable && (
          <ol className="mt-4 space-y-5">
            <li>
              <div className="flex items-baseline gap-3">
                <span className="t-mono t-tabular shrink-0 text-[12px] font-medium text-dark-signal">
                  01
                </span>
                <div className="min-w-0">
                  <h3 className="text-[14px] font-semibold text-dark-ink">Install LiteSPM</h3>
                  <p className="mt-0.5 text-[13px] leading-relaxed text-dark-ink-2">
                    One CLI for every agent you use.
                  </p>
                </div>
              </div>
              <DarkCmd
                text="npm install -g litespm"
                label="Copy install command"
                message="Install command copied"
              />
            </li>

            <li>
              <div className="flex items-baseline gap-3">
                <span className="t-mono t-tabular shrink-0 text-[12px] font-medium text-dark-signal">
                  02
                </span>
                <div className="min-w-0">
                  <h3 className="text-[14px] font-semibold text-dark-ink">
                    Connect {targeted ? agent?.label : "your agent"}
                  </h3>
                  <p className="mt-0.5 text-[13px] leading-relaxed text-dark-ink-2">
                    Run the setup wizard and pick your agent. It finds that agent&rsquo;s config file
                    and adds the one LiteSPM entry, keeping a backup of what was there.
                  </p>
                </div>
              </div>
              <DarkCmd text="litespm" label="Copy setup command" message="Setup command copied" />
            </li>

            <li id="pkg-step-add" tabIndex={-1} className="scroll-mt-16">
              <div className="flex items-baseline gap-3">
                <span className="t-mono t-tabular shrink-0 text-[12px] font-medium text-dark-signal">
                  03
                </span>
                <div className="min-w-0">
                  <h3 className="text-[14px] font-semibold text-dark-ink">Add {item.name}</h3>
                  <p className="mt-0.5 text-[13px] leading-relaxed text-dark-ink-2">
                    {targeted
                      ? `Installs this entry for ${agent?.label}.`
                      : "Installs this entry for the agent you connected in step 2."}
                  </p>
                </div>
              </div>
              <DarkCmd
                text={`${INSTALL_COMMAND_PREFIX}${item.id}`}
                label="Copy install command"
                message="Install command copied"
              />

              <p className="mt-2 text-[12px] leading-relaxed text-dark-ink-2">
                Or run <code className="t-mono text-dark-ink">/marketplace</code> inside your agent and
                search for &ldquo;{item.name}&rdquo;.
              </p>

              {item.installHint && (
                <div className="mt-2">
                  <p className="t-mono text-[10px] uppercase tracking-wider text-dark-ink-2">
                    {item.kind === "skill"
                      ? "Upstream source repository"
                      : "The host's own plugin command"}
                  </p>
                  <DarkCmd
                    text={item.installHint}
                    label="Copy host install command"
                    message="Host command copied"
                  />
                </div>
              )}
            </li>
          </ol>
        )}

        {!liteSPMInstallable && item.kind === "plugin" && (
          <div className="mt-3">
            {item.installHint ? (
              <>
                <p className="t-mono text-[10px] uppercase tracking-wider text-dark-ink-2">
                  The catalog&rsquo;s host command (outside LiteSPM)
                </p>
                <DarkCmd
                  text={item.installHint}
                  label="Copy host install command"
                  message="Host command copied"
                />
              </>
            ) : (
              <p className="text-[12px] leading-relaxed text-dark-ink-2">
                The catalog does not publish a host install command for this entry.
              </p>
            )}
          </div>
        )}

        <p className="mt-5 border-t border-dark-rule pt-3 text-[12px] leading-relaxed text-dark-ink-2">
          This page is static: it cannot tell whether LiteSPM is installed on your machine. Catalog
          installability is not a local status check.
        </p>
      </div>

      <ManualSetup />
    </section>
  );
}
