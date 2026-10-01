// Clipboard helper with a synchronous fallback for non-secure contexts
// (http://, denied permission, older browsers) and a tiny toast event bus.

export type ToastKind = "success" | "error";

export interface ToastEvent {
  id: number;
  message: string;
  kind: ToastKind;
}

const TOAST_EVENT = "litepsm-toast";
let toastSeq = 0;

export function emitToast(message: string, kind: ToastKind = "success"): void {
  if (typeof window === "undefined") return;
  toastSeq += 1;
  window.dispatchEvent(
    new CustomEvent<ToastEvent>(TOAST_EVENT, { detail: { id: toastSeq, message, kind } })
  );
}

export function onToast(handler: (t: ToastEvent) => void): () => void {
  if (typeof window === "undefined") return () => {};
  const listener = (e: Event) => handler((e as CustomEvent<ToastEvent>).detail);
  window.addEventListener(TOAST_EVENT, listener);
  return () => window.removeEventListener(TOAST_EVENT, listener);
}

function fallbackCopy(text: string): boolean {
  if (typeof document === "undefined") return false;
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.position = "fixed";
  ta.style.top = "-1000px";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  document.body.removeChild(ta);
  return ok;
}

/**
 * Copy text to the clipboard. Returns true on success and never throws.
 * Emits a toast on both success and failure so callers do not need alert().
 */
export async function copyText(text: string, successMessage = "Copied to clipboard"): Promise<boolean> {
  try {
    if (typeof navigator !== "undefined" && navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      emitToast(successMessage, "success");
      return true;
    }
  } catch {
    // fall through to the legacy path
  }
  const ok = fallbackCopy(text);
  emitToast(ok ? successMessage : "Copy failed - select and copy manually", ok ? "success" : "error");
  return ok;
}
