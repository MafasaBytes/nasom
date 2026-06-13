// Package core is the domain-generic heart of Houvast. It models the general problem:
// "a regulated change can silently invalidate prior compliance work across a portfolio."
//
// It knows NOTHING about nitrogen, AERIUS, mol/ha/jr, or IMAER — those live in
// internal/domains/nitrogen. This package depends on nothing outside the standard library.
// Do not import app, adapters, or any domain package here (see docs/ARCHITECTURE.md).
package core

import (
	"encoding/json"
	"time"
)

// ---- Identifiers -----------------------------------------------------------

type (
	// TenantID is the hard multi-tenant isolation boundary (see ADR-006).
	TenantID string
	// AssetID identifies a real-world monitored thing (a development project, site, permit holder).
	AssetID string
	// AssessmentID identifies a point-in-time compliance artifact for an asset.
	AssessmentID string
	// ChangeEventID identifies an external change (a rule version or a ruling).
	ChangeEventID string
	// DomainKey names a regulated vertical. MVP ships only "nitrogen" (see ADR-007).
	DomainKey string
)

const DomainNitrogen DomainKey = "nitrogen" // future: "pfas", "water", "co2"

// ---- Value types -----------------------------------------------------------

// GeoRef is a generic geographic reference. Domains interpret it (e.g. nitrogen attaches the
// nearest Natura 2000 area and distance via Asset.Metadata / Assessment.Inputs).
type GeoRef struct {
	Lat float64
	Lon float64
}

// DefensibilityStatus is the single user-facing signal the whole product turns on.
type DefensibilityStatus string

const (
	StatusDefensible DefensibilityStatus = "defensible" // valid against current version + case law
	StatusAttention  DefensibilityStatus = "attention"  // affected; review / likely remediation
	StatusExposed    DefensibilityStatus = "exposed"     // no longer defensible; action required
)

// RuleVersionRef identifies the authoritative model/rule version an assessment was computed under
// (e.g. nitrogen: "AERIUS 2025.3"). Opaque string + effective date; domains parse the semantics.
type RuleVersionRef struct {
	Domain      DomainKey
	Label       string
	EffectiveAt time.Time
}

// ChangeKind distinguishes the two forces that invalidate work over time.
type ChangeKind string

const (
	ChangeRuleVersion ChangeKind = "rule_version" // a new authoritative model/rule version
	ChangeCaseLaw     ChangeKind = "case_law"     // a relevant court ruling
)

// Delta is an optional numeric before/after captured in a Finding (e.g. deposition 0.06 -> 0.11).
type Delta struct {
	Metric string
	Old    float64
	New    float64
	Unit   string
}

// ---- Entities --------------------------------------------------------------

// Asset is a real-world thing being monitored. Domain-specific attributes live in Metadata
// (kept loose at the core boundary on purpose; the nitrogen domain reads/writes its own keys).
type Asset struct {
	ID               AssetID
	TenantID         TenantID
	Domain           DomainKey
	Name             string
	Location         GeoRef
	Metadata         map[string]string
	CapitalAtRiskEUR int64
	CreatedAt        time.Time
}

// AssessmentResult is the outcome of a domain calculation. EngineRef points to the persisted
// OFFICIAL engine output (e.g. AERIUS Connect job id / stored GML) — see ADR-001/002.
type AssessmentResult struct {
	Headline  string             // human summary, e.g. "0,06 mol/ha/jr op Veluwe"
	Metrics   map[string]float64 // domain metrics, e.g. {"deposition_mol_ha_yr": 0.06}
	EngineRef string             // pointer to persisted authoritative engine output
}

// Assessment is a point-in-time compliance artifact establishing defensibility against a specific
// rule version and case-law baseline.
//
// LIABILITY POSTURE (ADR-004): AuthoredBy records the customer/consultant as the author of record.
// Houvast is a tool, not the adviser. We surface Findings and recommendations, never guarantees.
type Assessment struct {
	ID              AssessmentID
	AssetID         AssetID
	TenantID        TenantID
	Domain          DomainKey
	AuthoredBy      string          // the customer/consultant — NOT Houvast (ADR-004)
	RuleVersion     RuleVersionRef  // the version this was computed under
	CaseLawBaseline time.Time       // case law known/valid at authoring time
	Inputs          json.RawMessage // domain-specific inputs, opaque to core
	Result          AssessmentResult
	Status          DefensibilityStatus
	CreatedAt       time.Time
}

// ChangeEvent is something in the world that may invalidate assessments. Payload carries the
// domain-specific detail (e.g. changed emission factors for a version, or the scope of a ruling).
type ChangeEvent struct {
	ID          ChangeEventID
	Domain      DomainKey
	Kind        ChangeKind
	Ref         string          // e.g. "AERIUS 2025.3" or an ECLI ruling id
	Summary     string          // short human description
	EffectiveAt time.Time
	IngestedAt  time.Time
	Payload     json.RawMessage // domain-specific change detail, opaque to core
}

// Finding is the result of evaluating one Assessment against one ChangeEvent — the unit of value.
type Finding struct {
	AssessmentID         AssessmentID
	ChangeEventID        ChangeEventID
	TenantID             TenantID
	PreviousStatus       DefensibilityStatus
	NewStatus            DefensibilityStatus
	Delta                *Delta // optional numeric delta (nil if not applicable)
	Explanation          string // "what changed", plain language
	Recommendation       string // suggested action — NOT a guarantee (ADR-004)
	EstimatedExposureEUR int64
	EvaluatedAt          time.Time
}

// ExposureSnapshot is the portfolio-level rollup shown on the monitor dashboard.
type ExposureSnapshot struct {
	TenantID          TenantID
	TotalAssets       int
	ExposedAssets     int
	AttentionAssets   int
	CapitalPipelineEUR int64
	CapitalAtRiskEUR  int64
	GeneratedAt       time.Time
}
