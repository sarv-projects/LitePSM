"use client";

import React, { useState } from "react";
import { Check, Copy } from "lucide-react";
import { copyText } from "../../lib/clipboard";

/**
 * The one copy affordance the package page uses, lifted out of the view so the
 * install panel, the manual-setup disclosure and the detail body can share it
 * without three identical implementations. Feedback is a label swap plus the
 * existing toast bus (`lib/clipboard`), never a modal.
 */
export function CopyButton({
  text,
  label,
  message,
  className = "btn shrink-0",
}: {
  text: string;
  label: string;
  message: string;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={async () => {
        if (await copyText(text, message)) {
          setCopied(true);
          window.setTimeout(() => setCopied(false), 2000);
        }
      }}
      aria-label={label}
      className={className}
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-ink" aria-hidden="true" />
      ) : (
        <Copy className="h-3.5 w-3.5" aria-hidden="true" />
      )}
      {copied ? "Copied" : "Copy"}
    </button>
  );
}
