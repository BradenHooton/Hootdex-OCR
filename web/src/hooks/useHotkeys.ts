import { useEffect, useRef } from "react";

export type HotkeyMap = Record<string, (e: KeyboardEvent) => void>;

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

/** Normalizes an event to "mod+enter", "alt+3", "j", "?" … ("mod" = Cmd on macOS, Ctrl elsewhere). */
export function comboOf(e: KeyboardEvent): string {
  const parts: string[] = [];
  if (isMac ? e.metaKey : e.ctrlKey) parts.push("mod");
  if (e.altKey) parts.push("alt");
  if (e.shiftKey && e.key.length > 1) parts.push("shift");
  // Alt+digit yields odd characters on macOS; use the physical key instead.
  const key = e.code.startsWith("Digit") ? e.code.slice(5) : e.key.toLowerCase();
  parts.push(key);
  return parts.join("+");
}

function inTextField(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName);
}

/**
 * Global keyboard shortcuts. Plain keys are ignored while typing in a field;
 * modifier combos (mod+enter, alt+1) work everywhere so the operator never leaves the keyboard.
 */
export function useHotkeys(map: HotkeyMap, enabled = true) {
  const ref = useRef(map);
  ref.current = map;
  useEffect(() => {
    if (!enabled) return;
    const handler = (e: KeyboardEvent) => {
      const combo = comboOf(e);
      const fn = ref.current[combo];
      if (!fn) return;
      const hasModifier = combo.includes("mod+") || combo.includes("alt+");
      if (!hasModifier && inTextField(e.target)) return;
      e.preventDefault();
      fn(e);
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [enabled]);
}

export const modLabel = isMac ? "⌘" : "Ctrl";
