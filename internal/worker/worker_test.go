package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/houvast/houvast/internal/adapters/memory"
	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen"
	"github.com/houvast/houvast/internal/domains/nitrogen/nitrogentest"
	"github.com/houvast/houvast/internal/domains/nitrogen/version"
	"github.com/houvast/houvast/internal/worker"
)

const dep = "deposition_mol_ha_yr"

// refClock is the injected wall clock for the whole cycle — deterministic IngestedAt / EvaluatedAt.
var refClock = time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return refClock }

// versionKeyedEngine returns deposition by the version label the event recomputes under:
//
//	"AERIUS Calculator 2024" -> 0.06 (== the prior result, so the cold-watermark 2024 event is a no-op)
//	"AERIUS Calculator 2025" -> 0.11 (> KDW 0.10, so the 2025 release flips the assessment to exposed)
//
// EXCEPT for the area that keeps a tenant under the KDW, which stays 0.04 regardless of version (used
// to prove cross-tenant isolation: same global event, that tenant never flips).
func versionKeyedEngine(underKDWArea string) *nitrogentest.FakeCalculationEngine {
	return &nitrogentest.FakeCalculationEngine{
		ResultFunc: func(inputs json.RawMessage, ver core.RuleVersionRef) (core.AssessmentResult, error) {
			var in nitrogen.NitrogenInputs
			_ = json.Unmarshal(inputs, &in)
			if in.Natura2000Area == underKDWArea {
				return core.AssessmentResult{Metrics: map[string]float64{dep: 0.04}}, nil
			}
			byVersion := map[string]float64{
				"AERIUS Calculator 2024": 0.06,
				"AERIUS Calculator 2025": 0.11,
			}
			return core.AssessmentResult{Metrics: map[string]float64{dep: byVersion[ver.Label]}}, nil
		},
	}
}

// buildWorld wires the full keep-alive stack against in-memory infra and the fake engine, with no
// Connect and no network. It returns the worker plus the repos/notifier so the test can assert state.
func buildWorld(t *testing.T, engine core.CalculationEngine) (*worker.Worker, *memory.AssessmentRepository, *memory.FindingRepository, *memory.Notifier, *memory.ChangeEventRepository) {
	t.Helper()
	reg := version.NewRegistry()
	watcher := nitrogen.NewAeriusReleaseWatcher(reg, fixedNow)
	deltas := nitrogen.NewRegistryVersionDeltaProvider(reg)

	dom := nitrogen.NewDomainWithSources(
		engine,
		nitrogen.StaticThresholdProvider{Default: &nitrogen.Threshold{KDW: 0.10, Unit: "mol/ha/jr"}},
		deltas,
		nitrogen.CuratedCaseLawScopeProvider{},
		nitrogen.InputsRouteDeriver{},
		fixedNow,
		watcher,
		nil, // no case-law source in M2
	)
	registry := app.NewRegistry(dom)

	assessments := memory.NewAssessmentRepository()
	portfolio := memory.NewPortfolioRepository()
	findings := memory.NewFindingRepository()
	changes := memory.NewChangeEventRepository()
	notifier := memory.NewNotifier()

	// The in-memory AssessmentRepository also serves as the app.TenantScope (IDs-only enumeration).
	svc := app.NewMonitorService(registry, assessments, assessments, portfolio, findings, changes, notifier)

	w := worker.New(core.DomainNitrogen, []core.RuleVersionSource{watcher}, changes, svc, assessments)
	return w, assessments, findings, notifier, changes
}

// seedTenant inserts a defensible assessment computed under "AERIUS Calculator 2024" (prior 0.06) plus
// its asset (carrying capital-at-risk for €exposure enrichment).
func seedTenant(t *testing.T, assessments *memory.AssessmentRepository, portfolio *memory.PortfolioRepository, tenant core.TenantID, area string, capital int64) {
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
		AuthoredBy:  "Consultancy BV", // ADR-004: the customer authors, never Houvast
		RuleVersion: core.RuleVersionRef{Domain: core.DomainNitrogen, Label: "AERIUS Calculator 2024"},
		Inputs:      inputs,
		Result:      core.AssessmentResult{Metrics: map[string]float64{dep: 0.06}},
		Status:      core.StatusDefensible,
	}); err != nil {
		t.Fatal(err)
	}
}

// M2 DoD — the keep-alive motion end-to-end through the Worker, with cross-tenant isolation.
//
// Two tenants, each with a defensible assessment under "AERIUS Calculator 2024". The worker polls the
// release watcher from a cold watermark, so BOTH the 2024 and 2025 releases are emitted. The fake
// engine returns 0.06 under 2024 (a natural no-op — equals the prior, no transition) and 0.11 under
// 2025 (over the 0.10 KDW -> exposed). Tenant B sits on an area that stays 0.04 under every version,
// so the same global event must leave B untouched. This proves both the keep-alive flip and isolation.
func TestRunOnce_KeepAliveFlip_And_TenantIsolation(t *testing.T) {
	ctx := context.Background()

	// Reconstruct the world but keep the portfolio handle for seeding.
	reg := version.NewRegistry()
	watcher := nitrogen.NewAeriusReleaseWatcher(reg, fixedNow)
	deltas := nitrogen.NewRegistryVersionDeltaProvider(reg)
	engine := versionKeyedEngine("Rijntakken") // tenant B's area stays under the KDW

	dom := nitrogen.NewDomainWithSources(
		engine,
		nitrogen.StaticThresholdProvider{Default: &nitrogen.Threshold{KDW: 0.10, Unit: "mol/ha/jr"}},
		deltas,
		nitrogen.CuratedCaseLawScopeProvider{},
		nitrogen.InputsRouteDeriver{},
		fixedNow,
		watcher,
		nil,
	)
	registry := app.NewRegistry(dom)

	assessments := memory.NewAssessmentRepository()
	portfolio := memory.NewPortfolioRepository()
	findings := memory.NewFindingRepository()
	changes := memory.NewChangeEventRepository()
	notifier := memory.NewNotifier()
	svc := app.NewMonitorService(registry, assessments, assessments, portfolio, findings, changes, notifier)
	w := worker.New(core.DomainNitrogen, []core.RuleVersionSource{watcher}, changes, svc, assessments)

	seedTenant(t, assessments, portfolio, "tenantA", "Veluwe", 5_000_000)     // will flip to exposed
	seedTenant(t, assessments, portfolio, "tenantB", "Rijntakken", 9_000_000) // stays defensible

	res, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// (a) Both releases were polled from the cold watermark, ascending.
	if len(res.Events) != 2 {
		t.Fatalf("res.Events = %d %v, want 2 (2024 + 2025 from cold watermark)", len(res.Events), eventRefs(res.Events))
	}
	if res.Events[0].Ref != "AERIUS Calculator 2024" || res.Events[1].Ref != "AERIUS Calculator 2025" {
		t.Fatalf("res.Events refs = %v, want [2024, 2025]", eventRefs(res.Events))
	}

	// (b) Exactly ONE finding across the whole cycle: tenant A flips on the 2025 event. The 2024 event
	// is a natural no-op (recompute 0.06 == prior 0.06 -> no transition -> no finding), which verifies
	// the watcher-emits-all-on-cold-watermark behavior is harmless.
	if len(res.Findings) != 1 {
		t.Fatalf("res.Findings = %d, want 1 (only the 2025 release flips tenant A)", len(res.Findings))
	}
	f := res.Findings[0]
	if f.TenantID != "tenantA" {
		t.Fatalf("finding tenant = %q, want tenantA", f.TenantID)
	}
	if f.NewStatus != core.StatusExposed || f.PreviousStatus != core.StatusDefensible {
		t.Fatalf("finding status %q->%q, want defensible->exposed", f.PreviousStatus, f.NewStatus)
	}
	if f.Delta == nil || f.Delta.Old != 0.06 || f.Delta.New != 0.11 {
		t.Fatalf("finding Delta = %+v, want 0.06->0.11", f.Delta)
	}
	if f.ChangeEventID != res.Events[1].ID {
		// The flip must be attributed to the 2025 event, not the 2024 one.
		t.Fatalf("finding ChangeEventID = %q, want the 2025 event id", f.ChangeEventID)
	}
	if f.EstimatedExposureEUR != 5_000_000 {
		t.Fatalf("finding €exposure = %d, want 5000000 (enriched from the asset)", f.EstimatedExposureEUR)
	}
	if !f.EvaluatedAt.Equal(refClock) {
		t.Fatalf("finding EvaluatedAt = %v, want injected clock %v", f.EvaluatedAt, refClock)
	}

	// (c) Persisted state: tenant A's assessment is now exposed.
	aAssess, _ := assessments.ListByDomain(ctx, "tenantA", core.DomainNitrogen)
	if len(aAssess) != 1 || aAssess[0].Status != core.StatusExposed {
		t.Fatalf("tenantA assessment = %+v, want 1 exposed", aAssess)
	}

	// (d) Per-tenant ExposureSnapshot reflects the exposed asset and surfaces capital-at-risk.
	snapA, ok := res.Snapshots["tenantA"]
	if !ok {
		t.Fatal("no snapshot for tenantA; the affected tenant must be snapshotted")
	}
	if snapA.ExposedAssets != 1 {
		t.Fatalf("tenantA snapshot ExposedAssets = %d, want 1", snapA.ExposedAssets)
	}
	if snapA.CapitalAtRiskEUR != 5_000_000 {
		t.Fatalf("tenantA snapshot CapitalAtRiskEUR = %d, want 5000000 (capital surfaced)", snapA.CapitalAtRiskEUR)
	}

	// (e) CROSS-TENANT ISOLATION. The SAME global events left tenant B fully untouched.
	bFindings, _ := findings.ListByTenant(ctx, "tenantB")
	if len(bFindings) != 0 {
		t.Fatalf("tenantB findings = %d, want 0 (isolation: B never flips on A's exposure)", len(bFindings))
	}
	bAssess, _ := assessments.ListByDomain(ctx, "tenantB", core.DomainNitrogen)
	if len(bAssess) != 1 || bAssess[0].Status != core.StatusDefensible {
		t.Fatalf("tenantB assessment = %+v, want 1 still defensible", bAssess)
	}
	if _, ok := res.Snapshots["tenantB"]; ok {
		t.Fatal("snapshot present for tenantB; only tenants with findings should be snapshotted")
	}
	// Repo-level isolation: tenant B cannot read tenant A's assessment by id.
	if _, err := assessments.GetAssessment(ctx, "tenantB", "assess-tenantA"); err == nil {
		t.Fatal("cross-tenant read of tenant A's assessment via tenant B succeeded — ADR-006 isolation broken")
	}

	// (f) Notifier fired exactly once, for tenant A only (isolation in the side-effect path too).
	if len(notifier.Sent) != 1 {
		t.Fatalf("notifier deliveries = %d, want 1", len(notifier.Sent))
	}
	if notifier.Sent[0].Tenant != "tenantA" {
		t.Fatalf("notification tenant = %q, want tenantA", notifier.Sent[0].Tenant)
	}

	// (g) No degraded errors on the happy path.
	if len(res.Errors) != 0 {
		t.Fatalf("res.Errors = %v, want none on the fake-engine happy path", res.Errors)
	}
}

// A second RunOnce after the watermark has advanced past the 2025 release must be a clean no-op:
// nothing re-polled, no new findings. Proves the worker advances its watermark and does not
// re-flip an already-exposed assessment on the next cycle.
func TestRunOnce_SecondCycle_IsNoOp(t *testing.T) {
	ctx := context.Background()
	engine := versionKeyedEngine("Rijntakken")
	w, assessments, findings, _, _ := buildWorld(t, engine)

	// Seed via the repos the world built — re-fetch by re-wiring a portfolio is not needed; seed
	// directly through a fresh portfolio is impossible here, so seed assessments + an asset inline.
	// buildWorld does not expose the portfolio, so this test only needs the assessment side for the
	// watermark/no-op behavior (no €enrichment asserted here).
	inputs, _ := json.Marshal(nitrogen.NitrogenInputs{Natura2000Area: "Veluwe"})
	if err := assessments.SaveAssessment(ctx, core.Assessment{
		ID: "assess-tenantA", AssetID: "asset-tenantA", TenantID: "tenantA", Domain: core.DomainNitrogen,
		AuthoredBy:  "Consultancy BV",
		RuleVersion: core.RuleVersionRef{Domain: core.DomainNitrogen, Label: "AERIUS Calculator 2024"},
		Inputs:      inputs,
		Result:      core.AssessmentResult{Metrics: map[string]float64{dep: 0.06}},
		Status:      core.StatusDefensible,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}
	first, _ := findings.ListByTenant(ctx, "tenantA")
	if len(first) != 1 {
		t.Fatalf("after cycle 1: tenantA findings = %d, want 1", len(first))
	}

	// Cycle 2: watermark now sits at the injected clock (IngestedAt of the saved events), which is
	// after every release effective date, so the watcher emits nothing.
	res2, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if len(res2.Events) != 0 {
		t.Fatalf("cycle 2 events = %d %v, want 0 (watermark advanced past 2025)", len(res2.Events), eventRefs(res2.Events))
	}
	if len(res2.Findings) != 0 {
		t.Fatalf("cycle 2 findings = %d, want 0 (no re-flip)", len(res2.Findings))
	}
	after, _ := findings.ListByTenant(ctx, "tenantA")
	if len(after) != 1 {
		t.Fatalf("after cycle 2: tenantA findings = %d, want still 1 (no duplicate finding)", len(after))
	}
}

// M2 step 4 — the GATED real engine path degrades gracefully (ADR-002/004). With the real
// AeriusConnectEngine (which returns ErrConnectGated), the worker must: complete the cycle, produce
// ZERO findings, leave every status UNTOUCHED (never default to defensible), and collect the gated
// error rather than crash.
func TestRunOnce_GatedEngine_DegradesGracefully(t *testing.T) {
	ctx := context.Background()

	reg := version.NewRegistry()
	watcher := nitrogen.NewAeriusReleaseWatcher(reg, fixedNow)
	deltas := nitrogen.NewRegistryVersionDeltaProvider(reg)

	// The real, gated engine — Compute returns ErrConnectGated (no Connect terms yet, ADR-001/002).
	var gated nitrogen.AeriusConnectEngine

	dom := nitrogen.NewDomainWithSources(
		&gated,
		nitrogen.StaticThresholdProvider{Default: &nitrogen.Threshold{KDW: 0.10, Unit: "mol/ha/jr"}},
		deltas,
		nitrogen.CuratedCaseLawScopeProvider{},
		nitrogen.InputsRouteDeriver{},
		fixedNow,
		watcher,
		nil,
	)
	registry := app.NewRegistry(dom)

	assessments := memory.NewAssessmentRepository()
	portfolio := memory.NewPortfolioRepository()
	findings := memory.NewFindingRepository()
	changes := memory.NewChangeEventRepository()
	notifier := memory.NewNotifier()
	svc := app.NewMonitorService(registry, assessments, assessments, portfolio, findings, changes, notifier)
	w := worker.New(core.DomainNitrogen, []core.RuleVersionSource{watcher}, changes, svc, assessments)

	inputs, _ := json.Marshal(nitrogen.NitrogenInputs{Natura2000Area: "Veluwe"})
	if err := portfolio.SaveAsset(ctx, core.Asset{ID: "asset-tenantA", TenantID: "tenantA", Domain: core.DomainNitrogen, CapitalAtRiskEUR: 5_000_000}); err != nil {
		t.Fatal(err)
	}
	if err := assessments.SaveAssessment(ctx, core.Assessment{
		ID: "assess-tenantA", AssetID: "asset-tenantA", TenantID: "tenantA", Domain: core.DomainNitrogen,
		AuthoredBy:  "Consultancy BV",
		RuleVersion: core.RuleVersionRef{Domain: core.DomainNitrogen, Label: "AERIUS Calculator 2024"},
		Inputs:      inputs,
		Result:      core.AssessmentResult{Metrics: map[string]float64{dep: 0.06}},
		Status:      core.StatusDefensible,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce returned a fatal error; gated engine must degrade, not crash: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("res.Findings = %d, want 0 (gated recompute produces no finding)", len(res.Findings))
	}
	if len(res.Errors) == 0 {
		t.Fatal("res.Errors is empty; the gated recompute must surface ErrConnectGated as a collected error")
	}
	foundGated := false
	for _, e := range res.Errors {
		if errors.Is(e, nitrogen.ErrConnectGated) {
			foundGated = true
			break
		}
	}
	if !foundGated {
		t.Fatalf("res.Errors = %v, want one wrapping nitrogen.ErrConnectGated", res.Errors)
	}

	// Status UNTOUCHED — never defaulted to defensible-or-otherwise (ADR-004).
	got, _ := assessments.GetAssessment(ctx, "tenantA", "assess-tenantA")
	if got.Status != core.StatusDefensible {
		t.Fatalf("status = %q, want unchanged (defensible); gated recompute must not alter status", got.Status)
	}
	if len(notifier.Sent) != 0 {
		t.Fatalf("notifier deliveries = %d, want 0 (no findings -> no notification)", len(notifier.Sent))
	}
}

func eventRefs(events []core.ChangeEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Ref
	}
	return out
}
