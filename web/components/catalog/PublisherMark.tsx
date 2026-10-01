"use client";

import React from "react";
import { Listing } from "../../lib/telemetry";
import { displayStars } from "../../lib/format";

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
 * Popularity is an upstream publisher-repo star count. It is never install
 * telemetry, so a missing value is stated rather than rendered as 0.
 */
export function StarCount({ stars, className = "" }: { stars: number; className?: string }) {
  const display = displayStars(stars);
  if (display.state === "absent") {
    return (
      <span className={`absent ${className}`} title="This release records no star count for this entry">
        not published
      </span>
    );
  }
  return (
    <span
      className={`t-mono t-tabular inline-flex items-center gap-1 text-[12px] text-pop ${className}`}
      title={`${display.value.toLocaleString("en-US")} publisher-repo stars. This is an upstream popularity signal, not LitePSM install telemetry.`}
    >
      <svg viewBox="0 0 10 10" className="h-2.5 w-2.5 fill-current" aria-hidden="true">
        <path d="M5 0l1.6 3.3 3.6.5-2.6 2.5.6 3.6L5 8.1 1.8 9.9l.6-3.6L-.2 3.8l3.6-.5L5 0z" />
      </svg>
      <span className="t-tabular">{display.label}</span>
      <span className="sr-only">publisher-repo stars, illustrative popularity</span>
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
