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

// Location checker (Surface B) request/response.
export interface CheckRequest {
  domain: DomainKey;
  inputs: unknown;                     // domain-specific (nitrogen: area, distance, homes, m2, intensity)
}
export interface CheckResult {
  result: AssessmentResult;
  status: DefensibilityStatus;
}
