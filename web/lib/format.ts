// Shared formatting helpers for the LitePSM Market UI.

const NUMBER = new Intl.NumberFormat("en-US");

/** Grouped integer: 5816 -> "5,816". Counts in this UI are always exact. */
export function formatCount(n: number): string {
  return NUMBER.format(Number.isFinite(n) ? n : 0);
}

/**
 * Stars are an upstream popularity signal copied from the publisher's repo, not
 * LitePSM install telemetry. A missing count must read as missing, never as 0,
 * so callers get a discriminated result instead of a misleading "0".
 */
export type StarsDisplay =
  | { state: "published"; value: number; label: string }
  | { state: "absent"; label: string };

export function displayStars(stars: number | null | undefined): StarsDisplay {
  if (typeof stars !== "number" || !Number.isFinite(stars) || stars <= 0) {
    return { state: "absent", label: "not published" };
  }
  return { state: "published", value: stars, label: formatStars(stars) };
}

/** Abbreviate a star count the way marketplaces do: 293270 -> "293k", 4200 -> "4.2k". */
export function formatStars(stars: number | null | undefined): string {
  const n = typeof stars === "number" && Number.isFinite(stars) ? stars : 0;
  if (n < 1000) return String(n);
  if (n < 10_000) return `${(n / 1000).toFixed(1).replace(/\.0$/, "")}k`;
  if (n < 1_000_000) return `${Math.round(n / 1000)}k`;
  return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
}

/** Short digest for a manifest reference: sha256:7f83b165… -> "sha256:7f83b1". */
export function shortDigest(digest: string | undefined): string | null {
  if (!digest) return null;
  const [alg, hex] = digest.split(":");
  if (!hex) return digest.slice(0, 12);
  return `${alg}:${hex.slice(0, 6)}`;
}

/** Normalize a listing slug into a safe JSON object key. */
export function jsonKey(slug: string): string {
  return (slug || "litepsm-server").replace(/[^A-Za-z0-9._-]/g, "-");
}

/** Normalize a listing slug into a safe TOML key (bare key or quoted). */
export function tomlKey(slug: string): string {
  const cleaned = (slug || "litepsm-server").replace(/[^A-Za-z0-9_-]/g, "_");
  const isBare = /^[A-Za-z0-9_-]+$/.test(cleaned);
  return isBare ? cleaned : `"${cleaned}"`;
}

/** Clamp long text with an ellipsis for card summaries. */
export function clamp(text: string, max: number): string {
  if (!text) return "";
  return text.length <= max ? text : `${text.slice(0, max - 1).trimEnd()}...`;
}
