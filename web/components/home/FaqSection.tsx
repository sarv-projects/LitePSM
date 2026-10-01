"use client";

import React, { useState } from "react";
import { ChevronDown } from "lucide-react";

const FAQS: Array<{ q: string; a: string }> = [
  {
    q: "What is LitePSM?",
    a: "LitePSM is a local package manager and federated catalog for AI agent capabilities — MCP servers, Agent Skills, and plugins. It connects once to each agent host and manages everything from a single local control plane.",
  },
  {
    q: "Do my API keys or tokens leave my machine?",
    a: "No. Credentials live in your operating system's native vault (Windows Credential Manager / DPAPI, macOS Keychain, Linux Secret Service). LitePSM's hosted catalog is read-only and never receives or proxies credentials.",
  },
  {
    q: "Which agents are supported?",
    a: "Claude Code, OpenAI Codex, OpenCode, Cline, and many more. Each host gets a single version-pinned litepsm bridge entry.",
  },
  {
    q: "Is installing a capability dangerous?",
    a: "Installation is passive: files are checksum-verified and unpacked into a content-addressed store with hard limits against path traversal, zip bombs, symlink escapes, and case collisions. No post-install scripts are executed.",
  },
  {
    q: "Can an AI agent install or run tools on its own?",
    a: "No. Effectful actions are fail-closed and require explicit, cryptographically bound user approval. Approvals are tied to the tool's schema fingerprint and content digest, so drift forces re-confirmation.",
  },
  {
    q: "How do I update the binary?",
    a: "Run `litepsm self-update`. It fetches the release manifest, verifies the SHA-256 checksum, and atomically replaces the binary.",
  },
];

export function FaqSection() {
  const [open, setOpen] = useState<number | null>(0);

  return (
    <section className="mx-auto w-full max-w-3xl px-4 pb-16 lg:px-8">
      <h2 className="mb-4 text-center text-lg font-bold tracking-tight text-slate-900">Frequently asked questions</h2>
      <div className="divide-y divide-slate-200 overflow-hidden rounded-2xl border border-slate-200 bg-white">
        {FAQS.map((item, i) => {
          const isOpen = open === i;
          return (
            <div key={item.q}>
              <button
                type="button"
                aria-expanded={isOpen}
                onClick={() => setOpen(isOpen ? null : i)}
                className="flex w-full items-center justify-between gap-4 px-5 py-4 text-left text-sm font-semibold text-slate-800 transition-colors hover:bg-slate-50"
              >
                {item.q}
                <ChevronDown
                  className={`h-4 w-4 shrink-0 text-slate-400 transition-transform ${isOpen ? "rotate-180" : ""}`}
                  aria-hidden="true"
                />
              </button>
              {isOpen && <p className="px-5 pb-4 text-xs leading-relaxed text-slate-600">{item.a}</p>}
            </div>
          );
        })}
      </div>
    </section>
  );
}
