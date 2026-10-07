import { readFileSync } from "node:fs";
import path from "node:path";

/**
 * The catalog rows as a static asset: `web/data/catalog.json`, byte for byte.
 *
 * This is the runtime half of the split that keeps ~3.8 MB out of the browser
 * bundle. The Server Component pages under `app/` read the same file at build
 * time and prerender its content, which never leaves the server; this handler
 * exists so the *browser* can fetch those exact rows later — on the first
 * search, filter or entry lookup — instead of receiving them as JavaScript on
 * first paint.
 *
 * `force-static` is what makes it part of the export: `next build` evaluates
 * the handler once and writes `out/data/catalog.json`. Reading the file rather
 * than importing the JSON keeps the emitted bytes identical to the source, so
 * the document the reader fetches and the snapshot the HTML was rendered from
 * are provably the same bytes.
 *
 * It deliberately lives at `/data/…` and not under `/v1/…`: that tree is the
 * released catalog produced by the Go builder and is re-materialized at deploy
 * time, so nothing the site authors own may be written there.
 */
export const dynamic = "force-static";

export function GET(): Response {
  const file = path.join(process.cwd(), "data", "catalog.json");
  return new Response(readFileSync(file), {
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Cache-Control": "public, max-age=0, must-revalidate",
      "X-Content-Type-Options": "nosniff",
    },
  });
}
