// Shared formatting helpers for the LitePSM Market UI.

const NUMBER = new Intl.NumberFormat("en-US");

/** Grouped integer: 5816 -> "5,816". Counts in this UI are always exact. */
export function formatCount(n: number): string {
  return NUMBER.format(Number.isFinite(n) ? n : 0);
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
