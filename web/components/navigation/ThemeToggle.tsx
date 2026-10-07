"use client";

import React, { useCallback, useEffect, useState } from "react";
import { Monitor, Moon, Sun } from "lucide-react";

/**
 * system / light / dark, cycled by one control, persisted in localStorage
 * under `litespm-theme`. There is no theme library: the value is a string the
 * inline script in `app/layout.tsx` reads before first paint, and CSS decides
 * what "system" means through `prefers-color-scheme`.
 *
 * The first render is always `system` on purpose. Hydrating from
 * localStorage would give the server and the client two different trees, so
 * the real choice lands in an effect — one frame of the default icon, and no
 * hydration mismatch.
 */
export type ThemeChoice = "system" | "light" | "dark";

const STORAGE_KEY = "litespm-theme";
const CYCLE: ThemeChoice[] = ["system", "light", "dark"];

const ICONS: Record<ThemeChoice, typeof Sun> = {
  system: Monitor,
  light: Sun,
  dark: Moon,
};

const LABELS: Record<ThemeChoice, string> = {
  system: "System",
  light: "Light",
  dark: "Dark",
};

function apply(choice: ThemeChoice) {
  if (typeof document === "undefined") return;
  document.documentElement.dataset.theme = choice;
}

export function ThemeToggle() {
  const [choice, setChoice] = useState<ThemeChoice>("system");

  useEffect(() => {
    try {
      const stored = window.localStorage.getItem(STORAGE_KEY);
      setChoice(stored === "light" || stored === "dark" ? stored : "system");
    } catch {
      // Storage denied (private mode, blocked cookies): system it is, and the
      // control still cycles — it just will not remember.
      setChoice("system");
    }
  }, []);

  const cycle = useCallback(() => {
    setChoice((current) => {
      const next = CYCLE[(CYCLE.indexOf(current) + 1) % CYCLE.length];
      apply(next);
      try {
        if (next === "system") window.localStorage.removeItem(STORAGE_KEY);
        else window.localStorage.setItem(STORAGE_KEY, next);
      } catch {
        // Persisting is best-effort; the session still changes theme.
      }
      return next;
    });
  }, []);

  const Icon = ICONS[choice];
  const next = CYCLE[(CYCLE.indexOf(choice) + 1) % CYCLE.length];

  return (
    <button
      type="button"
      onClick={cycle}
      className="btn btn-icon"
      title={`Theme: ${LABELS[choice].toLowerCase()}. Activate for ${LABELS[next].toLowerCase()}.`}
      aria-label={`Theme: ${LABELS[choice]}. Activate to switch to ${LABELS[next]}.`}
    >
      <Icon className="h-4 w-4" aria-hidden="true" />
    </button>
  );
}
