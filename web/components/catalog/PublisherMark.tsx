"use client";

import React from "react";
import { Listing } from "../../lib/telemetry";

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
 * `publisher.verified` is a registry flag on the publisher, not a LitePSM
 * security audit, so it is rendered in ink with the wording spelled out.
 */
/**
 * `publisher.verified` is a registry flag on the publisher, not a LitePSM
 * security audit, so it is rendered in ink with the wording spelled out. The
 * glyph is optional: in a dense row it collides with the meta separator, so
 * rows carry the word alone.
 */
export function VerifiedMark({ glyph = false }: { glyph?: boolean }) {
  return (
    <span
      className={glyph ? "inline-flex items-center gap-1.5 text-ink-2" : "text-ink-2"}
      title="The upstream registry marks this publisher as verified. This is not a LitePSM security audit."
    >
      {glyph && <span aria-hidden="true" className="inline-block h-1.5 w-1.5 rotate-45 bg-ink" />}
      verified
    </span>
  );
}
