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
      className="pointer-events-none fixed bottom-6 left-1/2 z-[60] flex -translate-x-1/2 flex-col items-center gap-2"
    >
      {toasts.map((t) => (
        <div
          key={t.id}
          className={`flex items-center gap-2 rounded-xl border px-4 py-2 text-xs font-medium shadow-lg backdrop-blur ${
            t.kind === "error"
              ? "border-rose-200 bg-rose-50/95 text-rose-800"
              : "border-emerald-200 bg-emerald-50/95 text-emerald-800"
          }`}
        >
          {t.kind === "error" ? (
            <AlertTriangle className="h-4 w-4" aria-hidden="true" />
          ) : (
            <CheckCircle2 className="h-4 w-4" aria-hidden="true" />
          )}
          {t.message}
        </div>
      ))}
    </div>
  );
}
