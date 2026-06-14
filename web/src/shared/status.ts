// The single source of truth for how DefensibilityStatus is presented (DESIGN §1.2, §2.7).
// Status is the hero signal: every view derives label, icon shape, and colour tokens from HERE,
// so colour is never the only cue and a new status variant becomes a compile error (exhaustive switch).

import type { DefensibilityStatus } from "../types/api";

export type StatusShape = "circle" | "diamond" | "triangle";

export interface StatusPresentation {
  status: DefensibilityStatus;
  /** Canonical NL pill label. */
  label: string;
  /** Non-colour identity: the shape the glyph sits in. */
  shape: StatusShape;
  /** The glyph inside the shape. */
  glyph: string;
  /** CSS custom-property names for this status (defined in tokens.css). */
  tokens: {
    fg: string;
    bg: string;
    line: string;
    solid: string;
  };
}

/**
 * Exhaustive mapping. The `switch` has no default and the function return type is
 * StatusPresentation, so adding a new DefensibilityStatus member fails to compile until
 * it is handled here — preventing a silent gap in the hero signal.
 */
export function statusPresentation(status: DefensibilityStatus): StatusPresentation {
  switch (status) {
    case "defensible":
      return {
        status,
        label: "Defensibel",
        shape: "circle",
        glyph: "✓",
        tokens: {
          fg: "var(--hv-status-defensible-fg)",
          bg: "var(--hv-status-defensible-bg)",
          line: "var(--hv-status-defensible-line)",
          solid: "var(--hv-status-defensible-solid)",
        },
      };
    case "attention":
      return {
        status,
        label: "Aandacht — herzien",
        shape: "diamond",
        glyph: "!",
        tokens: {
          fg: "var(--hv-status-attention-fg)",
          bg: "var(--hv-status-attention-bg)",
          line: "var(--hv-status-attention-line)",
          solid: "var(--hv-status-attention-solid)",
        },
      };
    case "exposed":
      return {
        status,
        label: "Niet meer defensibel",
        shape: "triangle",
        glyph: "!",
        tokens: {
          fg: "var(--hv-status-exposed-fg)",
          bg: "var(--hv-status-exposed-bg)",
          line: "var(--hv-status-exposed-line)",
          solid: "var(--hv-status-exposed-solid)",
        },
      };
    default: {
      // If a new DefensibilityStatus is added, this line is a compile error (TS2322).
      const exhaustive: never = status;
      return exhaustive;
    }
  }
}

/** True when the status warrants the "at risk" euro treatment + alarm chrome. */
export function isAtRisk(status: DefensibilityStatus): boolean {
  return status !== "defensible";
}
