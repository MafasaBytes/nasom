package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/houvast/houvast/internal/core"
)

// TenantScope enumerates the tenants a GLOBAL ChangeEvent must fan across. It lives in app (not
// core) and returns tenant IDs only — never cross-tenant data — so the core's "no cross-tenant
// read" contract (ADR-006) is preserved. This is the explicit, auditable fan-out path ADR-006
// anticipates. See ADR-011.
type TenantScope interface {
	TenantsForDomain(ctx context.Context, domain core.DomainKey) ([]core.TenantID, error)
}

type monitorService struct {
	registry    core.DomainRegistry
	tenants     TenantScope
	assessments core.AssessmentRepository
	portfolio   core.PortfolioRepository
	findings    core.FindingRepository
	changes     core.ChangeEventRepository
	notifier    core.Notifier
}

// NewMonitorService wires the monitor. tenants is typically backed by the same store as
// assessments (the in-memory AssessmentRepository implements TenantScope).
func NewMonitorService(
	registry core.DomainRegistry,
	tenants TenantScope,
	assessments core.AssessmentRepository,
	portfolio core.PortfolioRepository,
	findings core.FindingRepository,
	changes core.ChangeEventRepository,
	notifier core.Notifier,
) MonitorService {
	return &monitorService{
		registry:    registry,
		tenants:     tenants,
		assessments: assessments,
		portfolio:   portfolio,
		findings:    findings,
		changes:     changes,
		notifier:    notifier,
	}
}

// OnChangeEvent fans a (global) ChangeEvent across every tenant's assessments in the event's
// domain, evaluates each via the domain's ImpactEvaluator, persists a Finding and updates status
// on a transition, and notifies tenants with new findings. Each tenant is processed in isolation
// (ADR-006). An Evaluate error leaves that assessment's status UNTOUCHED — never defaulting to
// defensible — and is collected for retry/escalation; other assessments still proceed.
func (s *monitorService) OnChangeEvent(ctx context.Context, e core.ChangeEvent) ([]core.Finding, error) {
	dom, ok := s.registry.Get(e.Domain)
	if !ok {
		return nil, fmt.Errorf("no domain registered for %q", e.Domain)
	}
	evaluator := dom.ImpactEvaluator()

	if err := s.changes.Save(ctx, e); err != nil {
		return nil, fmt.Errorf("persist change event: %w", err)
	}

	tenants, err := s.tenants.TenantsForDomain(ctx, e.Domain)
	if err != nil {
		return nil, fmt.Errorf("enumerate tenants: %w", err)
	}

	var all []core.Finding
	var errs []error

	for _, t := range tenants {
		assessments, err := s.assessments.ListByDomain(ctx, t, e.Domain)
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: list assessments: %w", t, err))
			continue
		}

		var tenantFindings []core.Finding
		for _, a := range assessments {
			f, err := evaluator.Evaluate(ctx, a, e)
			if err != nil {
				// Leave status untouched; never default to defensible. Record and continue.
				errs = append(errs, fmt.Errorf("tenant %s: evaluate %s: %w", t, a.ID, err))
				continue
			}
			if f.NewStatus == f.PreviousStatus {
				continue // no transition → no finding, no status write
			}
			if f.NewStatus == core.StatusExposed || f.NewStatus == core.StatusAttention {
				// Enrich €exposure from the asset's capital-at-risk (orchestration context the
				// pure judge() does not see).
				if asset, gerr := s.portfolio.GetAsset(ctx, t, a.AssetID); gerr == nil {
					f.EstimatedExposureEUR = asset.CapitalAtRiskEUR
				}
			}
			if err := s.findings.Save(ctx, f); err != nil {
				errs = append(errs, fmt.Errorf("tenant %s: save finding %s: %w", t, a.ID, err))
				continue
			}
			if err := s.assessments.UpdateStatus(ctx, t, a.ID, f.NewStatus); err != nil {
				errs = append(errs, fmt.Errorf("tenant %s: update status %s: %w", t, a.ID, err))
				continue
			}
			tenantFindings = append(tenantFindings, f)
		}

		if len(tenantFindings) > 0 {
			if err := s.notifier.NotifyExposure(ctx, t, tenantFindings); err != nil {
				errs = append(errs, fmt.Errorf("tenant %s: notify: %w", t, err))
			}
			all = append(all, tenantFindings...)
		}
	}

	if len(errs) > 0 {
		return all, errors.Join(errs...)
	}
	return all, nil
}

// PortfolioExposure rolls up a tenant's current exposure for the dashboard.
func (s *monitorService) PortfolioExposure(ctx context.Context, t core.TenantID) (core.ExposureSnapshot, error) {
	assets, err := s.portfolio.ListAssets(ctx, t)
	if err != nil {
		return core.ExposureSnapshot{}, err
	}
	snap := core.ExposureSnapshot{TenantID: t, TotalAssets: len(assets), GeneratedAt: time.Now()}
	capByAsset := make(map[core.AssetID]int64, len(assets))
	for _, a := range assets {
		snap.CapitalPipelineEUR += a.CapitalAtRiskEUR
		capByAsset[a.ID] = a.CapitalAtRiskEUR
	}
	// M1: nitrogen is the only domain (ADR-007). core.DomainNitrogen is a core constant — not an
	// import of the nitrogen package, so no layer violation. Generalize when a second domain exists.
	assessments, err := s.assessments.ListByDomain(ctx, t, core.DomainNitrogen)
	if err != nil {
		return core.ExposureSnapshot{}, err
	}
	for _, a := range assessments {
		switch a.Status {
		case core.StatusExposed:
			snap.ExposedAssets++
			snap.CapitalAtRiskEUR += capByAsset[a.AssetID]
		case core.StatusAttention:
			snap.AttentionAssets++
		}
	}
	return snap, nil
}

// FindingsForAssessment returns the change history for one assessment (drawer view).
func (s *monitorService) FindingsForAssessment(ctx context.Context, t core.TenantID, id core.AssessmentID) ([]core.Finding, error) {
	return s.findings.ListByAssessment(ctx, t, id)
}

var _ MonitorService = (*monitorService)(nil)
