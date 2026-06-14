// Resolving a Finding to the ChangeEvent that produced it.
//
// CONTRACT GAP (verified against the live API, 2026-06): the HTTP layer currently serializes
// ChangeEvent.id and Finding.changeEventId as "" (empty) for the curated demo events — the worker
// does not assign stable ids before fanning them out. So a naive id-join (events.get(changeEventId))
// is unreliable: every event collapses onto the "" key. We therefore resolve defensively:
//   1. exact non-empty id match, else
//   2. the event's ref string appearing in the finding's explanation/recommendation (the curated
//      findings cite their ECLI/version inline), else
//   3. content-based kind inference (ECLI/ABRvS → case_law) with no concrete event.
// This keeps the drawer correct even while the id contract is incomplete.

import type { ChangeEvent, ChangeKind, Finding } from "../../types/api";

export interface ResolvedSource {
  kind: ChangeKind;
  /** The concrete event when one could be matched (gives ref + effectiveAt for the chip). */
  event: ChangeEvent | undefined;
}

function inferKind(finding: Finding): ChangeKind {
  const text = `${finding.explanation} ${finding.recommendation}`;
  return /ECLI|ABRvS|RvS|Raad van State|uitspraak/i.test(text) ? "case_law" : "rule_version";
}

export function resolveSource(finding: Finding, events: readonly ChangeEvent[]): ResolvedSource {
  // 1. exact id match (works once the backend assigns stable ids).
  if (finding.changeEventId !== "") {
    const byId = events.find((e) => e.id === finding.changeEventId);
    if (byId) return { kind: byId.kind, event: byId };
  }
  // 2. ref cited in the finding text.
  const haystack = `${finding.explanation} ${finding.recommendation}`;
  const byRef = events.find((e) => e.ref !== "" && haystack.includes(e.ref));
  if (byRef) return { kind: byRef.kind, event: byRef };

  // 3. fall back to content inference; if exactly one event of that kind exists, use it for the chip.
  const kind = inferKind(finding);
  const sameKind = events.filter((e) => e.kind === kind);
  return { kind, event: sameKind.length === 1 ? sameKind[0] : undefined };
}
