"use client";

import React, { useEffect, useState } from "react";
import { CheckCircle2, AlertTriangle } from "lucide-react";
import { onToast, ToastEvent } from "../../lib/clipboard";

/** Global toast host. Mounted once in the root layout. */
export function ToastViewport() {
  const [toasts, setToasts] = useState<ToastEvent[]>([]);

  useEffect(() => {
    return onToast((t) => {
      setToasts((prev) => [...prev.slice(-2), t]);
      window.setTimeout(() => {
        setToasts((prev) => prev.filter((x) => x.id !== t.id));
      }, 2200);
    });
  }, []);

  return (
    <div
      role="status"
      aria-live="polite"
      className="pointer-events-none fixed bottom-5 left-1/2 z-[60] flex -translate-x-1/2 flex-col items-center gap-2"
    >
      {toasts.map((t) => (
        <div
          key={t.id}
          className={`flex items-center gap-2 rounded-ctl border px-3 py-2 text-[12px] font-medium ${
            t.kind === "error"
              ? "border-pop bg-pop text-white"
              : "border-ink bg-ink text-surface"
          }`}
        >
          {t.kind === "error" ? (
            <AlertTriangle className="h-3.5 w-3.5" aria-hidden="true" />
          ) : (
            <CheckCircle2 className="h-3.5 w-3.5" aria-hidden="true" />
          )}
          {t.message}
        </div>
      ))}
    </div>
  );
}
