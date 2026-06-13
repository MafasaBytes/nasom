package app

import (
	"context"
	"fmt"

	"github.com/houvast/houvast/internal/core"
)

// indicativeEngineRef marks an AssessmentResult that came from the INDICATIVE pre-check rather than
// an authoritative engine computation (ADR-001). A promoted assessment carrying this sentinel is
// PROVISIONAL: it is in the monitored portfolio but is NOT yet Connect-backed, so it is
// distinguishable from an officially-computed artifact (whose EngineRef points to a persisted RIVM
// AERIUS Connect output). A later slice recomputes it via CalculationEngine and replaces this ref.
const indicativeEngineRef = "indicative"

// IDGenerator produces fresh identifiers for promoted assets/assessments. Injected so Promote is
// deterministic/testable (a test supplies a counter; production supplies UUIDs).
type IDGenerator interface {
	NewID() string
}

type checkService struct {
	registry core.DomainRegistry
	assets   core.PortfolioRepository
	assess   core.AssessmentRepository
	clock    core.Clock
	ids      IDGenerator
}

// NewCheckService wires Surface B (the location checker / on-ramp). The clock and id generator are
// injected so Promote is deterministic and tenant-isolation (ADR-006) is enforced via the
// tenant-scoped repositories.
func NewCheckService(
	registry core.DomainRegistry,
	assets core.PortfolioRepository,
	assess core.AssessmentRepository,
	clock core.Clock,
	ids IDGenerator,
) CheckService {
	return &checkService{
		registry: registry,
		assets:   assets,
		assess:   assess,
		clock:    clock,
		ids:      ids,
	}
}

// Check runs the domain's INDICATIVE pre-check (ADR-001) and maps the CheckOutcome to a CheckResult.
// It is gate-free: it never calls the authoritative CalculationEngine.
func (s *checkService) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	dom, ok := s.registry.Get(req.Domain)
	if !ok {
		return CheckResult{}, fmt.Errorf("no domain registered for %q", req.Domain)
	}
	checker := dom.LocationChecker()
	if checker == nil {
		return CheckResult{}, fmt.Errorf("domain %q has no location checker", req.Domain)
	}

	out, err := checker.Check(ctx, req.Inputs)
	if err != nil {
		return CheckResult{}, fmt.Errorf("indicative check: %w", err)
	}

	return CheckResult{
		Result:      out.Result,
		Status:      verdictToStatus(out.Verdict),
		Verdict:     out.Verdict,
		Mitigations: out.Mitigations,
	}, nil
}

// Promote turns a checked site into a monitored Asset + Assessment in the tenant's portfolio
// (the "check once, watched forever" on-ramp). All writes are scoped to req.Tenant (ADR-006).
//
// ADR-004: AuthoredBy is the customer/consultant passed in — NEVER Houvast.
// ADR-001: the promoted assessment is NOT yet Connect-backed. We flag it PROVISIONAL by stamping
// the result's EngineRef with the indicative sentinel, so it is distinguishable from an officially
// computed artifact. Its status is derived from the indicative result. A later slice recomputes via
// the authoritative CalculationEngine before the assessment is treated as decision-bearing.
func (s *checkService) Promote(ctx context.Context, req CheckRequest, name, authoredBy string, result core.AssessmentResult) (core.AssetID, error) {
	if authoredBy == "" {
		// ADR-004: the customer/consultant is the author of record; never default to Houvast.
		return "", fmt.Errorf("authoredBy is required (the customer/consultant of record, ADR-004)")
	}

	now := s.clock.Now()
	assetID := core.AssetID(s.ids.NewID())
	assessmentID := core.AssessmentID(s.ids.NewID())

	// Re-derive the indicative status/verdict from the supplied indicative result so the persisted
	// assessment carries a consistent status.
	verdict := s.indicativeVerdict(ctx, req)
	status := verdictToStatus(verdict)

	asset := core.Asset{
		ID:        assetID,
		TenantID:  req.Tenant,
		Domain:    req.Domain,
		Name:      name,
		Metadata:  map[string]string{},
		CreatedAt: now,
	}
	if err := s.assets.SaveAsset(ctx, asset); err != nil {
		return "", fmt.Errorf("save asset: %w", err)
	}

	// ADR-001: mark the result PROVISIONAL — not an authoritative engine output.
	provisional := result
	provisional.EngineRef = indicativeEngineRef

	assessment := core.Assessment{
		ID:         assessmentID,
		AssetID:    assetID,
		TenantID:   req.Tenant,
		Domain:     req.Domain,
		AuthoredBy: authoredBy, // ADR-004: customer/consultant, never Houvast
		Inputs:     req.Inputs,
		Result:     provisional,
		Status:     status,
		CreatedAt:  now,
	}
	if err := s.assess.SaveAssessment(ctx, assessment); err != nil {
		return "", fmt.Errorf("save assessment: %w", err)
	}

	return assetID, nil
}

// indicativeVerdict re-runs the domain's pre-check to obtain the verdict for the promoted inputs. If
// the checker is unavailable or errors, it falls back to permit-required — the conservative posture
// (never silently "buildable"/defensible, ADR-004).
func (s *checkService) indicativeVerdict(ctx context.Context, req CheckRequest) core.CheckVerdict {
	dom, ok := s.registry.Get(req.Domain)
	if !ok {
		return core.VerdictPermitRequired
	}
	checker := dom.LocationChecker()
	if checker == nil {
		return core.VerdictPermitRequired
	}
	out, err := checker.Check(ctx, req.Inputs)
	if err != nil {
		return core.VerdictPermitRequired
	}
	return out.Verdict
}

// verdictToStatus maps the indicative buildability verdict to the user-facing DefensibilityStatus:
// buildable→defensible, with-mitigation→attention, permit-required→exposed. This is an INDICATIVE
// signal for the UI only (ADR-001), not an authoritative status.
func verdictToStatus(v core.CheckVerdict) core.DefensibilityStatus {
	switch v {
	case core.VerdictBuildable:
		return core.StatusDefensible
	case core.VerdictBuildableWithMitigation:
		return core.StatusAttention
	case core.VerdictPermitRequired:
		return core.StatusExposed
	default:
		return core.StatusExposed // conservative default (ADR-004): never silently defensible
	}
}

var _ CheckService = (*checkService)(nil)
