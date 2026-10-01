"use client";

import { useEffect, useLayoutEffect, useRef, useState } from "react";

/**
 * A small count-up figure.
 *
 * Three constraints shaped this:
 *
 *  1. The static html must contain the REAL number. A counter that renders "0"
 *     server-side and only becomes correct after hydration makes the page lie
 *     to crawlers, to no-JS readers, and to anyone reading view-source. So the
 *     server render is the final value, and the animation is an enhancement on
 *     top of it.
 *
 *  2. No layout shift. The element is sized to the final string up front, so a
 *     growing number cannot reflow the line it sits on.
 *
 *  3. It must not fight `prefers-reduced-motion`. When motion is reduced the
 *     value is simply present.
 */
export function CountUp({
  value,
  durationMs = 900,
  className = "",
}: {
  value: number;
  durationMs?: number;
  className?: string;
}) {
  const ref = useRef<HTMLSpanElement>(null);
  const [display, setDisplay] = useState(value);
  const started = useRef(false);

  // Before first paint on the client, drop to zero so the roll-up is visible.
  // useLayoutEffect (not useEffect) so this happens before the browser paints
  // and there is no flash of the final number.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const reduced =
      typeof window !== "undefined" &&
      window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    if (reduced || typeof IntersectionObserver === "undefined") {
      setDisplay(value);
      return;
    }
    setDisplay(0);
  }, [value]);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    if (reduced || typeof IntersectionObserver === "undefined") {
      setDisplay(value);
      return;
    }

    let raf = 0;
    let start = 0;

    const run = (now: number) => {
      if (!start) start = now;
      const t = Math.min(1, (now - start) / durationMs);
      // easeOutCubic: fast start, settled finish.
      const eased = 1 - Math.pow(1 - t, 3);
      setDisplay(Math.round(value * eased));
      if (t < 1) raf = requestAnimationFrame(run);
      else setDisplay(value);
    };

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && !started.current) {
            started.current = true;
            raf = requestAnimationFrame(run);
            observer.disconnect();
          }
        }
      },
      { threshold: 0.4 }
    );
    observer.observe(el);

    return () => {
      observer.disconnect();
      cancelAnimationFrame(raf);
    };
  }, [value, durationMs]);

  const finalText = value.toLocaleString("en-US");

  return (
    <span
      ref={ref}
      className={`t-tabular ${className}`}
      // Reserve the final width so the roll-up cannot reflow its line.
      style={{ minWidth: `${finalText.length}ch`, display: "inline-block" }}
    >
      {display.toLocaleString("en-US")}
    </span>
  );
}
