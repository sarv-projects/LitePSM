"use client";

import React from "react";
import { Listing } from "../../lib/telemetry";

import { Check } from "lucide-react";

/** Single-letter stamp. 2px radius: a chip, not a card. */
export function PublisherTile({ name, kind }: { name?: string; kind: Listing["kind"] }) {
  const initial = (name || "?").trim().charAt(0).toUpperCase();
  return (
    <span className="tile" data-kind={kind} aria-hidden="true">
      {initial}
    </span>
  );
}

/**
 * `publisher.verified` is a registry flag on the publisher, not a LiteSPM
 * security audit. Rendered as a distinct verified badge.
 */
export function VerifiedMark({ glyph = true }: { glyph?: boolean }) {
  return (
    <span
      className="inline-flex items-center gap-1 rounded-[2px] bg-accent-wash px-1.5 py-0.5 text-[10px] font-medium text-accent border border-accent/20"
      title="The upstream registry marks this publisher as verified. This is not a LiteSPM security audit."
    >
      {glyph && <Check className="h-2.5 w-2.5 stroke-[2.5]" aria-hidden="true" />}
      verified
    </span>
  );
}
