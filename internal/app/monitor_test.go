package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/houvast/houvast/internal/adapters/memory"
	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen"
	"github.com/houvast/houvast/internal/domains/nitrogen/nitrogentest"
)

const dep = "deposition_mol_ha_yr"

// DoD #2 — a global ChangeEvent that exposes tenant A produces ZERO findings/changes in tenant B.
func TestOnChangeEvent_CrossTenantIsolation(t *testing.T) {
	ctx := context.Background()
	ref := time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)

	// Fake engine: recomputed deposition depends on the assessment's area, so under the SAME global
	// version event tenant A (Veluwe) crosses the KDW while tenant B (Rijntakken) stays under it.
	engine := &nitrogentest.FakeCalculationEngine{
		ResultFunc: func(inputs json.RawMessage, version core.RuleVersionRef) (core.AssessmentResult, error) {
			var in nitrogen.NitrogenInputs
			_ = json.Unmarshal(inputs, &in)
			byArea := map[string]float64{"Veluwe": 0.11, "Rijntakken": 0.04}
			return core.AssessmentResult{Metrics: map[string]float64{dep: byArea[in.Natura2000Area]}}, nil
		},
	}
	dom := nitrogen.NewDomain(
		engine,
		nitrogen.StaticThresholdProvider{Default: &nitrogen.Threshold{KDW: 0.10, Unit: "mol/ha/jr"}},
		nitrogen.StaticVersionDeltaProvider{},
		nitrogen.CuratedCaseLawScopeProvider{},
		nitrogen.InputsRouteDeriver{},
		func() time.Time { return ref },
	)
	registry := app.NewRegistry(dom)

	assessments := memory.NewAssessmentRepository()
	portfolio := memory.NewPortfolioRepository()
	findings := memory.NewFindingRepository()
	changes := memory.NewChangeEventRepository()
	notifier := memory.NewNotifier()

	seed(t, portfolio, assessments, "tenantA", "Veluwe", 5_000_000)     // will become exposed
	seed(t, portfolio, assessments, "tenantB", "Rijntakken", 9_000_000) // stays defensible

	// The in-memory AssessmentRepository also serves as the TenantScope (it can enumerate tenant IDs).
	svc := app.NewMonitorService(registry, assessments, assessments, portfolio, findings, changes, notifier)

	e := core.ChangeEvent{ID: "c1", Domain: core.DomainNitrogen, Kind: core.ChangeRuleVersion, Ref: "AERIUS 2025.3", IngestedAt: ref}
	got, err := svc.OnChangeEvent(ctx, e)
	if err != nil {
		t.Fatalf("OnChangeEvent: %v", err)
	}

	// Tenant A: exactly one exposed finding, with €exposure enriched from the asset.
	if len(got) != 1 {
		t.Fatalf("returned findings = %d, want 1", len(got))
	}
	aFindings, _ := findings.ListByTenant(ctx, "tenantA")
	if len(aFindings) != 1 || aFindings[0].NewStatus != core.StatusExposed {
		t.Fatalf("tenantA findings = %+v, want 1 exposed", aFindings)
	}
	if aFindings[0].EstimatedExposureEUR != 5_000_000 {
		t.Fatalf("tenantA exposure = %d, want 5000000", aFindings[0].EstimatedExposureEUR)
	}

	// Tenant B: ZERO findings, status untouched — isolation holds.
	bFindings, _ := findings.ListByTenant(ctx, "tenantB")
	if len(bFindings) != 0 {
		t.Fatalf("tenantB findings = %d, want 0 (cross-tenant isolation)", len(bFindings))
	}
	bAssess, _ := assessments.ListByDomain(ctx, "tenantB", core.DomainNitrogen)
	if len(bAssess) != 1 || bAssess[0].Status != core.StatusDefensible {
		t.Fatalf("tenantB assessment changed: %+v", bAssess)
	}

	// Repo-level isolation: tenant B cannot read tenant A's assessment by id.
	if _, err := assessments.GetAssessment(ctx, "tenantB", "assess-tenantA"); err == nil {
		t.Fatal("cross-tenant read of tenant A's assessment via tenant B succeeded — isolation broken")
	}
}

func seed(t *testing.T, portfolio *memory.PortfolioRepository, assessments *memory.AssessmentRepository, tenant core.TenantID, area string, capital int64) {
	t.Helper()
	ctx := context.Background()
	assetID := core.AssetID("asset-" + string(tenant))
	if err := portfolio.SaveAsset(ctx, core.Asset{
		ID: assetID, TenantID: tenant, Domain: core.DomainNitrogen, Name: area, CapitalAtRiskEUR: capital,
	}); err != nil {
		t.Fatal(err)
	}
	inputs, _ := json.Marshal(nitrogen.NitrogenInputs{Natura2000Area: area})
	if err := assessments.SaveAssessment(ctx, core.Assessment{
		ID: core.AssessmentID("assess-" + string(tenant)), AssetID: assetID, TenantID: tenant, Domain: core.DomainNitrogen,
		AuthoredBy:  "Consultancy BV",
		RuleVersion: core.RuleVersionRef{Domain: core.DomainNitrogen, Label: "AERIUS 2024"},
		Inputs:      inputs,
		Result:      core.AssessmentResult{Metrics: map[string]float64{dep: 0.06}},
		Status:      core.StatusDefensible,
	}); err != nil {
		t.Fatal(err)
	}
}
