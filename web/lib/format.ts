// Shared formatting helpers for the LitePSM Market UI.

/** Abbreviate a star count the way marketplaces do: 293270 -> "293k", 4200 -> "4.2k". */
export function formatStars(stars: number | null | undefined): string {
  const n = typeof stars === "number" && Number.isFinite(stars) ? stars : 0;
  if (n < 1000) return String(n);
  if (n < 10_000) return `${(n / 1000).toFixed(1).replace(/\.0$/, "")}k`;
  if (n < 1_000_000) return `${Math.round(n / 1000)}k`;
  return `${(n / 1_000_000).toFixed(1).replace(/\.0$/, "")}M`;
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
