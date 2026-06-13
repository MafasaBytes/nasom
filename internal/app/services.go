// Package app holds the application services (use-cases). It orchestrates the core ports and
// contains no infrastructure (no SQL, no HTTP, no AERIUS specifics). Implementations are wired in
// cmd/ (the composition root). See docs/ARCHITECTURE.md.
package app

import (
	"context"
	"encoding/json"

	"github.com/houvast/houvast/internal/core"
)

// MonitorService is Surface A: the defensibility monitor.
type MonitorService interface {
	// OnChangeEvent fans a ChangeEvent across all affected assessments in the relevant domain,
	// evaluates each via the domain's ImpactEvaluator, persists Findings, updates statuses, and
	// notifies affected tenants. Returns the findings produced. Driven by the worker.
	OnChangeEvent(ctx context.Context, e core.ChangeEvent) ([]core.Finding, error)

	// PortfolioExposure returns the current exposure snapshot for a tenant (dashboard rollup).
	PortfolioExposure(ctx context.Context, t core.TenantID) (core.ExposureSnapshot, error)

	// FindingsForAssessment returns the change history/explanations for one assessment (drawer view).
	FindingsForAssessment(ctx context.Context, t core.TenantID, id core.AssessmentID) ([]core.Finding, error)
}

// CheckRequest is the input to the location pre-check (Surface B). Inputs are domain-specific and
// passed opaquely to the domain's CalculationEngine.
type CheckRequest struct {
	Tenant core.TenantID
	Domain core.DomainKey
	Inputs json.RawMessage
}

// CheckResult is the INDICATIVE pre-check outcome surfaced to the user (Surface B).
//
// ADR-001: this is NOT authoritative. Status is mapped from the indicative Verdict for the UI; a
// promoted, decision-bearing assessment must still be backed by an official engine computation.
// ADR-004: Verdict and Mitigations are indicative options, never guarantees.
type CheckResult struct {
	Result      core.AssessmentResult
	Status      core.DefensibilityStatus // mapped from Verdict for the UI signal
	Verdict     core.CheckVerdict        // coarse buildability triage from the pre-check
	Mitigations []string                 // indicative mitigation options that fit the verdict
}

// CheckService is Surface B: the location checker / on-ramp into the monitor.
type CheckService interface {
	// Check runs an indicative pre-check for a candidate site.
	// NOTE: a pre-check may use an indicative model for speed, but any assessment promoted into the
	// monitor as a decision-bearing artifact must be backed by an official engine computation
	// (ADR-001) and authored by the customer (ADR-004).
	Check(ctx context.Context, req CheckRequest) (CheckResult, error)

	// Promote turns a checked site into a monitored Asset + Assessment in the tenant's portfolio.
	// authoredBy MUST be the customer/consultant of record (ADR-004), never Houvast.
	Promote(ctx context.Context, req CheckRequest, name, authoredBy string, result core.AssessmentResult) (core.AssetID, error)
}
