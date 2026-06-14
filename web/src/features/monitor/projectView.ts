// View-model derivation for a PortfolioProject (asset + latest assessment + latest finding).
// Keeps the display logic (status resolution, metadata extraction, indicative marking) in one place,
// typed against api.ts.

import type { DefensibilityStatus, PortfolioProject } from "../../types/api";

export interface ProjectView {
  assetId: string;
  assessmentId: string | undefined;
  name: string;
  /** "Zaandam · 220 woningen + 4.500 m²" style location/programme line. */
  location: string;
  natura2000: string | undefined;
  distanceKm: number | undefined;
  status: DefensibilityStatus;
  /** Current deposition metric (mol/ha/jr), if present. */
  deposition: number | undefined;
  ruleVersionLabel: string | undefined;
  authoredAtIso: string;
  capitalEur: number;
  /** ADR-004: a promoted pre-check not yet backed by an official AERIUS computation. */
  indicative: boolean;
  hasFinding: boolean;
}

const DEPOSITION_METRIC = "deposition_mol_ha_yr";

function num(meta: Record<string, string>, key: string): number | undefined {
  const raw = meta[key];
  if (raw === undefined) return undefined;
  const n = Number(raw);
  return Number.isFinite(n) ? n : undefined;
}

function buildLocation(meta: Record<string, string>): string {
  const parts: string[] = [];
  const homes = num(meta, "homes");
  const m2 = num(meta, "m2");
  if (homes !== undefined) parts.push(`${new Intl.NumberFormat("nl-NL").format(homes)} woningen`);
  if (m2 !== undefined)
    parts.push(`${new Intl.NumberFormat("nl-NL").format(m2)} m² commercieel`);
  return parts.join(" + ");
}

export function toProjectView(p: PortfolioProject): ProjectView {
  const { asset, latestAssessment, latestFinding } = p;
  const meta = asset.metadata ?? {};

  // Status precedence: the latest assessment's status is authoritative (the worker writes the
  // post-finding status back onto the assessment). Fall back to the finding, then defensible.
  const status: DefensibilityStatus =
    latestAssessment?.status ?? latestFinding?.newStatus ?? "defensible";

  const deposition =
    latestAssessment?.result.metrics?.[DEPOSITION_METRIC] ?? undefined;

  // ADR-004 honesty: a promoted pre-check not yet backed by an official AERIUS Connect computation
  // is stamped engineRef === "indicative" by the backend (verified against the live API). An authored
  // seed assessment has engineRef === "" — that is NOT indicative, it is an authored artifact whose
  // engine output reference is simply not surfaced here. Only the explicit sentinel marks indicative.
  const indicative =
    (latestAssessment?.result.engineRef ?? "").toLowerCase() === "indicative";

  return {
    assetId: asset.id,
    assessmentId: latestAssessment?.id,
    name: asset.name,
    location: buildLocation(meta),
    natura2000: meta["natura2000"],
    distanceKm: num(meta, "distance_km"),
    status,
    deposition,
    ruleVersionLabel: latestAssessment?.ruleVersionLabel,
    authoredAtIso: latestAssessment?.createdAt ?? asset.createdAt,
    capitalEur: asset.capitalAtRiskEur,
    indicative,
    hasFinding: latestFinding !== null,
  };
}
