// API contract types — the TypeScript mirror of the Go core DTOs (internal/core/entities.go).
// Keep in sync with the backend by hand for now; consider codegen (e.g. from OpenAPI) later.
// These describe what the HTTP API (internal/adapters/httpapi) exposes to the web frontend.

export type TenantId = string;
export type AssetId = string;
export type AssessmentId = string;
export type ChangeEventId = string;

export type DomainKey = "nitrogen"; // future: "pfas" | "water" | "co2"

export type DefensibilityStatus = "defensible" | "attention" | "exposed";

export type ChangeKind = "rule_version" | "case_law";

export interface Delta {
  metric: string;
  old: number;
  new: number;
  unit: string;
}

export interface AssessmentResult {
  headline: string;                    // e.g. "0,06 mol/ha/jr op Veluwe"
  metrics: Record<string, number>;     // e.g. { deposition_mol_ha_yr: 0.06 }
  engineRef: string;                   // pointer to persisted official engine output
}

export interface Asset {
  id: AssetId;
  tenantId: TenantId;
  domain: DomainKey;
  name: string;
  metadata: Record<string, string>;    // domain-specific (e.g. natura2000, homes, m2)
  capitalAtRiskEur: number;
  createdAt: string;                   // ISO-8601
}

export interface Assessment {
  id: AssessmentId;
  assetId: AssetId;
  tenantId: TenantId;
  domain: DomainKey;
  authoredBy: string;                  // the customer/consultant — NOT Houvast (ADR-004)
  ruleVersionLabel: string;            // e.g. "AERIUS 2025.3"
  result: AssessmentResult;
  status: DefensibilityStatus;
  createdAt: string;
}

export interface ChangeEvent {
  id: ChangeEventId;
  domain: DomainKey;
  kind: ChangeKind;
  ref: string;                         // "AERIUS 2025.3" | ECLI id
  summary: string;
  effectiveAt: string;
}

// The unit of value: one assessment evaluated against one change event.
export interface Finding {
  assessmentId: AssessmentId;
  changeEventId: ChangeEventId;
  previousStatus: DefensibilityStatus;
  newStatus: DefensibilityStatus;
  delta?: Delta;                       // optional numeric before/after
  explanation: string;                 // "what changed"
  recommendation: string;              // suggested action — never a guarantee (ADR-004)
  estimatedExposureEur: number;
  evaluatedAt: string;
}

// Dashboard rollup (Surface A).
export interface ExposureSnapshot {
  tenantId: TenantId;
  totalAssets: number;
  exposedAssets: number;
  attentionAssets: number;
  capitalPipelineEur: number;
  capitalAtRiskEur: number;
  generatedAt: string;
}

// Per-asset dashboard read model: GET /api/portfolio returns PortfolioProject[].
// (Mirrors httpapi.portfolioProjectDTO — an asset joined with its latest assessment and
// latest finding. latestAssessment/latestFinding are null when the asset has none yet.)
export interface PortfolioProject {
  asset: Asset;
  latestAssessment: Assessment | null;
  latestFinding: Finding | null;
}

// Coarse buildability verdict of the INDICATIVE Surface B pre-check (ADR-001).
// Mirrors core.CheckVerdict. Maps to a DefensibilityStatus for the UI signal.
export type CheckVerdict =
  | "buildable"
  | "buildable_with_mitigation"
  | "permit_required";

// Location checker (Surface B) request/response.
export interface CheckRequest {
  domain: DomainKey;
  inputs: unknown;                     // domain-specific (nitrogen: area, distance, homes, m2, intensity)
}

// Domain-specific nitrogen inputs (mirrors nitrogen.NitrogenInputs JSON tags).
// Opaque to core; sent as CheckRequest.inputs.
export interface NitrogenInputs {
  natura2000_area: string;
  distance_km: number;
  homes: number;
  commercial_m2: number;
  build_intensity: number;             // intensity multiplier (light 1 → heavy 2.2)
  routes?: string[];                   // offsetting routes, e.g. ["intern_salderen"]
}

// Mirrors httpapi.checkResultDTO. ADR-001: indicative, never authoritative.
// ADR-004: verdict/mitigations are options, never guarantees.
export interface CheckResult {
  result: AssessmentResult;
  status: DefensibilityStatus;
  verdict: CheckVerdict;
  mitigations: string[];
}

// POST /api/promote request body (mirrors httpapi.promoteRequestDTO).
// ADR-004: authoredBy is the customer/consultant of record — required, never Houvast.
export interface PromoteRequest {
  domain: DomainKey;
  inputs: unknown;
  name: string;
  authoredBy: string;
  result: AssessmentResult;
}

// POST /api/promote response (mirrors httpapi.promoteResponseDTO).
export interface PromoteResponse {
  assetId: AssetId;
}

// POST /api/ingest response (DEV/ADMIN — reproduces the demo flip).
// Mirrors httpapi.ingestResponseDTO. The collected `errors` are EXPECTED graceful
// degradation (e.g. the gated AERIUS Connect recompute, ADR-002), not failures.
export interface IngestResult {
  events: ChangeEvent[];
  findings: Finding[];
  snapshots: ExposureSnapshot[];
  errors: string[];
}
