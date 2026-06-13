package core

import (
	"context"
	"encoding/json"
	"time"
)

// This file defines the PORTS (interfaces) of the hexagon. The core depends only on these
// abstractions; adapters in internal/domains/* and internal/adapters/* implement them.
//
// Two families:
//   - Domain ports   : what a regulated vertical (nitrogen, ...) must provide.
//   - Driven ports   : storage and notification the app needs from infrastructure.

// ===== Domain ports =========================================================

// CalculationEngine computes a domain assessment result from inputs, under a given rule version.
//
// NITROGEN IMPLEMENTATION (ADR-001/002): an arms-length HTTP client to the official RIVM AERIUS
// Connect API. It MUST NOT embed/fork the AERIUS engine (AGPLv3 §13). The authoritative result is
// RIVM-computed; the adapter persists it immediately because Connect results expire (~3 days).
type CalculationEngine interface {
	// Compute submits domain-specific inputs to the authoritative engine and returns the result.
	Compute(ctx context.Context, inputs json.RawMessage, version RuleVersionRef) (AssessmentResult, error)
	// Name identifies the engine (for audit/provenance), e.g. "aerius-connect".
	Name() string
}

// RuleVersionSource detects and ingests new authoritative model/rule versions for a domain.
//
// NITROGEN: watches the (annual) AERIUS release; emits ChangeEvent{Kind: ChangeRuleVersion}
// carrying the version delta (changed emission factors / dispersion / background maps).
type RuleVersionSource interface {
	// Current returns the currently authoritative version for the domain.
	Current(ctx context.Context) (RuleVersionRef, error)
	// Poll returns rule-version change events that became effective since the given time.
	Poll(ctx context.Context, since time.Time) ([]ChangeEvent, error)
}

// CaseLawSource ingests relevant rulings for a domain.
//
// NITROGEN: Raad van State feed + curated mapping of doctrine changes (e.g. intern salderen,
// 18 Dec 2024) into machine-actionable scope. Emits ChangeEvent{Kind: ChangeCaseLaw}.
type CaseLawSource interface {
	Poll(ctx context.Context, since time.Time) ([]ChangeEvent, error)
}

// CheckVerdict is the coarse buildability signal of an INDICATIVE location pre-check (Surface B).
// It is a triage signal for the on-ramp, NOT an authoritative compliance result (see ADR-001).
type CheckVerdict string

const (
	// VerdictBuildable: the pre-check indicates the site is likely buildable as-is.
	VerdictBuildable CheckVerdict = "buildable"
	// VerdictBuildableWithMitigation: likely buildable if mitigation options are applied.
	VerdictBuildableWithMitigation CheckVerdict = "buildable_with_mitigation"
	// VerdictPermitRequired: the pre-check indicates a permit / further assessment is required.
	VerdictPermitRequired CheckVerdict = "permit_required"
)

// CheckOutcome is the result of an INDICATIVE location pre-check (Surface B on-ramp).
//
// AUTHORITATIVE BOUNDARY (ADR-001): this is explicitly NOT an authoritative assessment. The
// authoritative, decision-bearing number must always come from an official CalculationEngine
// computation. A pre-check may use a fast indicative model for speed,
// but a promoted, decision-bearing artifact must still be backed by a real engine computation, and
// the customer is its author (ADR-004). Indicative is always true — it documents that boundary.
type CheckOutcome struct {
	Indicative  bool             // always true: a pre-check is NEVER authoritative (ADR-001)
	Result      AssessmentResult // the indicative estimate (NOT engine-backed; EngineRef is not authoritative)
	Verdict     CheckVerdict     // coarse buildability triage signal
	Mitigations []string         // indicative mitigation options that fit the verdict — NOT guarantees (ADR-004)
}

// LocationChecker runs an INDICATIVE pre-check for a candidate site (Surface B). It is a fast,
// gate-free triage capability a domain MAY provide; it is NOT the authoritative CalculationEngine.
//
// ADR-001: the result is indicative only. Promoting a checked site into the monitor as a
// decision-bearing assessment requires an official CalculationEngine computation; the pre-check
// never substitutes for it.
type LocationChecker interface {
	// Check returns an indicative outcome for domain-specific inputs (opaque to core).
	//
	// CONTRACT: Check MUST be a pure, deterministic function of inputs — no clock, no randomness,
	// no network. Callers (e.g. CheckService.Promote) may re-derive the verdict from the same inputs
	// and MUST get the same outcome the user saw at Check time. A non-deterministic implementation
	// would let a promoted assessment's status silently disagree with the displayed pre-check.
	Check(ctx context.Context, inputs json.RawMessage) (CheckOutcome, error)
}

// ImpactEvaluator decides whether and how a ChangeEvent affects a given Assessment.
// This is the heart of the product and is necessarily DOMAIN-SPECIFIC. For a rule-version change it
// may recompute via CalculationEngine; for case law it matches the ruling scope against the
// assessment's route/inputs. It returns a Finding describing the new status, delta, explanation,
// recommendation, and estimated exposure.
type ImpactEvaluator interface {
	Evaluate(ctx context.Context, a Assessment, e ChangeEvent) (Finding, error)
}

// Domain bundles the adapters for one regulated vertical. Adding PFAS/water/CO2 later means
// implementing this interface — NOT changing the core (ADR-007).
type Domain interface {
	Key() DomainKey
	CalculationEngine() CalculationEngine
	RuleVersionSource() RuleVersionSource
	CaseLawSource() CaseLawSource
	ImpactEvaluator() ImpactEvaluator
	// LocationChecker provides the INDICATIVE Surface B pre-check (ADR-001). It MAY return nil for a
	// monitor-only wiring; callers must handle a nil checker. It is never authoritative.
	LocationChecker() LocationChecker
}

// DomainRegistry resolves a DomainKey to its Domain implementation (composition wiring lives in cmd/).
type DomainRegistry interface {
	Get(key DomainKey) (Domain, bool)
}

// ===== Driven ports (infrastructure) ========================================
//
// All repositories are TENANT-SCOPED by contract (ADR-006). Implementations MUST enforce isolation;
// there is intentionally no cross-tenant read on these interfaces. Any cross-tenant signal must go
// through a separate, explicit, audited aggregate path (the network-effect layer, built later).

type PortfolioRepository interface {
	ListAssets(ctx context.Context, t TenantID) ([]Asset, error)
	GetAsset(ctx context.Context, t TenantID, id AssetID) (Asset, error)
	SaveAsset(ctx context.Context, a Asset) error
}

type AssessmentRepository interface {
	GetAssessment(ctx context.Context, t TenantID, id AssessmentID) (Assessment, error)
	ListByAsset(ctx context.Context, t TenantID, asset AssetID) ([]Assessment, error)
	// ListByDomain returns every assessment in a domain for a tenant — used to fan out a ChangeEvent.
	ListByDomain(ctx context.Context, t TenantID, domain DomainKey) ([]Assessment, error)
	SaveAssessment(ctx context.Context, a Assessment) error
	UpdateStatus(ctx context.Context, t TenantID, id AssessmentID, status DefensibilityStatus) error
}

type ChangeEventRepository interface {
	Save(ctx context.Context, e ChangeEvent) error
	// LastIngested returns the watermark for incremental polling per domain/kind.
	LastIngested(ctx context.Context, domain DomainKey, kind ChangeKind) (time.Time, error)
}

type FindingRepository interface {
	Save(ctx context.Context, f Finding) error
	ListByTenant(ctx context.Context, t TenantID) ([]Finding, error)
	ListByAssessment(ctx context.Context, t TenantID, id AssessmentID) ([]Finding, error)
}

// Notifier delivers exposure alerts to a tenant (email/webhook/in-app).
type Notifier interface {
	NotifyExposure(ctx context.Context, t TenantID, findings []Finding) error
}

// Clock is injected so time-dependent logic is testable.
type Clock interface{ Now() time.Time }
