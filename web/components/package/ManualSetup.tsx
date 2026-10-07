"use client";

import React, { useEffect, useState } from "react";
import { ChevronDown, Info } from "lucide-react";
import { HOSTS, PlatformOS, bridgeSnippet, hostPath } from "../../lib/hosts";
import { CopyButton } from "./CopyButton";

/**
 * "Manual setup" — the generic host × platform block that used to render as a
 * full section on every package page. It answers the same question on all
 * 5,825 entries (one `litespm` bridge entry per host; the snippet is identical
 * regardless of the capability being viewed), so ARCH/25 §7.3 moves it behind a
 * single disclosure instead of repeating it per page.
 *
 * Two rules this component exists to keep:
 *   1. The raw config snippet — the only TOML/JSON on the route — exists only
 *      while the disclosure is open. It is never above the fold.
 *   2. Paths and formats come from the generated registry (`lib/hosts.ts`); a
 *      platform whose Windows path the registry refuses to guess falls back to
 *      the documented Unix form and says so, rather than inventing one.
 */
export function ManualSetup() {
  const [activeHost, setActiveHost] = useState(HOSTS[0].id);
  const [platformOs, setPlatformOs] = useState<PlatformOS>("linux");

  useEffect(() => {
    const ua = typeof navigator !== "undefined" ? navigator.userAgent.toLowerCase() : "";
    setPlatformOs(ua.includes("mac") ? "mac" : ua.includes("linux") ? "linux" : "win");
  }, []);

  const host = HOSTS.find((h) => h.id === activeHost) ?? HOSTS[0];
  const snippet = bridgeSnippet(host, "litespm", platformOs);

  return (
    <details className="group mt-4 border-t border-rule">
      <summary className="flex min-h-[44px] cursor-pointer list-none items-center justify-between gap-2 text-[13px] font-medium text-ink-2 transition-colors duration-state ease-out hover:text-ink md:min-h-[40px]">
        Manual setup
        <span className="flex items-center gap-2 text-[12px] font-normal text-ink-3">
          write the bridge entry yourself
          <ChevronDown
            className="h-3.5 w-3.5 transition-transform duration-panel ease-out group-open:rotate-180"
            aria-hidden="true"
          />
        </span>
      </summary>

      <div className="pb-2">
        <p className="max-w-prose text-[13px] leading-relaxed text-ink-2">
          Skip the guided setup and edit the file yourself. LiteSPM writes one{" "}
          <code className="t-mono text-ink">litespm</code> entry into the host&rsquo;s config — pick
          the agent and your platform to see the exact file and the entry it holds.
        </p>

        <div className="section-head mt-5">
          <div>
            <h3 className="text-[13px] font-semibold tracking-tight text-ink">Config file</h3>
            <p className="section-note mt-0.5">One bridge entry per host.</p>
          </div>
          <div className="flex shrink-0 items-center gap-0.5 border border-ink-3 bg-sunken p-0.5">
            {(["win", "mac", "linux"] as PlatformOS[]).map((o) => (
              <button
                key={o}
                type="button"
                aria-pressed={platformOs === o}
                onClick={() => setPlatformOs(o)}
                className={`h-6 rounded-[3px] px-2.5 text-[11px] font-medium transition-colors ${
                  platformOs === o ? "bg-ink text-surface" : "text-ink-2 hover:text-ink"
                }`}
              >
                {o === "win" ? "Windows" : o === "mac" ? "macOS" : "Linux"}
              </button>
            ))}
          </div>
        </div>

        <div className="no-scrollbar mt-4 flex gap-1.5 overflow-x-auto pb-1" role="group" aria-label="Agent host">
          {HOSTS.map((h) => (
            <button
              key={h.id}
              type="button"
              aria-pressed={activeHost === h.id}
              onClick={() => setActiveHost(h.id)}
              className="chip shrink-0"
            >
              {h.name}
            </button>
          ))}
        </div>

        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          <div>
            <p className="t-mono text-[10px] text-ink-3">Config file</p>
            <div className="mt-1 flex items-start gap-2">
              <code className="t-mono min-w-0 flex-1 break-all border border-rule-2 bg-sunken px-2.5 py-2 text-[11px] text-ink-2">
                {hostPath(host, platformOs)}
              </code>
              <CopyButton
                text={hostPath(host, platformOs)}
                label="Copy configuration file path"
                message="Config path copied"
              />
            </div>
          </div>

          <div>
            <p className="t-mono text-[10px] text-ink-3">Config format</p>
            <p className="t-mono mt-1 border border-rule-2 bg-sunken px-2.5 py-2 text-[11px] uppercase text-ink-2">
              {host.kind}
              {host.nested && <span className="ml-2 normal-case text-ink-3">nested v2 layout</span>}
            </p>
          </div>
        </div>

        <div className="mt-4">
          <h4 className="text-[13px] font-semibold text-ink">
            Snippet
            <span className="ml-2 font-normal text-ink-3">LiteSPM bridge</span>
          </h4>

          <div className="on-dark mt-2 overflow-hidden rounded-panel bg-dark">
            <div className="flex items-center justify-between gap-2 border-b border-dark-rule px-3 py-2">
              <span className="t-mono truncate text-[11px] text-dark-ink-2">
                {host.name} · {host.kind === "toml" ? "config.toml" : "config.json"}
              </span>
              <CopyButton text={snippet} label="Copy configuration snippet" message="Snippet copied" />
            </div>
            <pre className="max-h-80 overflow-auto whitespace-pre-wrap p-3.5 font-mono text-[12px] leading-relaxed text-dark-ink">
              {snippet}
            </pre>
          </div>

          <p className="mt-2.5 flex items-start gap-2 text-[12px] leading-relaxed text-ink-3">
            <Info className="mt-px h-3.5 w-3.5 shrink-0" aria-hidden="true" />
            <span>
              Running <code className="t-mono text-ink-2">litespm</code> backs up the target file and
              injects this single entry, preserving existing entries —{" "}
              <code className="t-mono text-ink-2">litespm host setup {host.id}</code> does the same
              thing from the CLI, and{" "}
              <code className="t-mono text-ink-2">litespm restore</code> puts the original file back.
            </span>
          </p>
        </div>
      </div>
    </details>
  );
}
