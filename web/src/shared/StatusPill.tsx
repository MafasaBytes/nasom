import type { CSSProperties } from "react";
import type { DefensibilityStatus } from "../types/api";
import { statusPresentation } from "./status";
import { StatusIcon } from "./StatusIcon";
import "./StatusPill.css";

interface StatusPillProps {
  status: DefensibilityStatus;
}

// The canonical status component (DESIGN §2.7): [icon-in-shape] [label], tinted.
// Reused in cards, drawer header, verdict echo. NEVER ships without its label and icon —
// status is communicated by label + shape + colour, never colour alone (WCAG, ADR-004 clarity).
export function StatusPill({ status }: StatusPillProps) {
  const p = statusPresentation(status);
  const style = {
    "--pill-fg": p.tokens.fg,
    "--pill-bg": p.tokens.bg,
  } as CSSProperties;

  return (
    <span className="hv-pill" style={style}>
      <StatusIcon shape={p.shape} glyph={p.glyph} color={p.tokens.fg} size={14} filled={false} />
      {p.label}
    </span>
  );
}
