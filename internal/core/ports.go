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
