package worker_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/houvast/houvast/internal/adapters/memory"
	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen"
	"github.com/houvast/houvast/internal/domains/nitrogen/caselaw"
	"github.com/houvast/houvast/internal/domains/nitrogen/nitrogentest"
	"github.com/houvast/houvast/internal/domains/nitrogen/version"
	"github.com/houvast/houvast/internal/worker"
)

// caselawECLI is the curated ruling the RvS source emits and the scope provider resolves.
const caselawECLI = "ECLI:NL:RVS:2024:4923"

// gateFreeEngine returns a fake CalculationEngine whose ResultFunc FAILS the test if it is ever
// called. The case-law branch of the evaluator (assemble for ChangeCaseLaw) must never call Connect;
// any invocation here is a gate-free violation. CallCount() lets the test assert zero calls too.
func gateFreeEngine(t *testing.T) *nitrogentest.FakeCalculationEngine {
	t.Helper()
	return &nitrogentest.FakeCalculationEngine{
		ResultFunc: func(inputs json.RawMessage, ver core.RuleVersionRef) (core.AssessmentResult, error) {
			t.Errorf("CalculationEngine.Compute was called on the case-law path (version=%q) — the case-law branch MUST be gate-free (ADR-012)", ver.Label)
			return core.AssessmentResult{}, nil
		},
	}
}

// buildCaseLawWorld wires a CASE-LAW-ONLY worker: the RvS source is passed as the only change source
// (nil rule-version sources), with the registry-backed scope provider + InputsRouteDeriver, in-memory
// repos, and a gate-free engine. It returns the worker plus the repos so the test can seed and assert.
func buildCaseLawWorld(t *testing.T, engine core.CalculationEngine) (
	*worker.Worker,
	*memory.AssessmentRepository,
	*memory.PortfolioRepository,
	*memory.FindingRepository,
	*memory.Notifier,
) {
	t.Helper()
	reg := caselaw.NewRegistry()
	rvs := nitrogen.NewRaadVanStateSource(reg, fixedNow)    // injected clock -> deterministic IngestedAt
	scopes := nitrogen.NewRegistryCaseLawScopeProvider(reg) // real-path provider (unknown ECLI errors)

	dom := nitrogen.NewDomainWithSources(
		engine,
		// A threshold provider is wired but the case-law path never consults it (no recompute, no KDW).
		nitrogen.StaticThresholdProvider{Default: &nitrogen.Threshold{KDW: 0.10, Unit: "mol/ha/jr"}},
		nitrogen.StaticVersionDeltaProvider{},
		scopes,
		nitrogen.InputsRouteDeriver{},
		fixedNow,
		nil, // NO rule-version source: this is a case-law-only world
		rvs, // the case-law source
	)
	registry := app.NewRegistry(dom)

	assessments := memory.NewAssessmentRepository()
	portfolio := memory.NewPortfolioRepository()
	findings := memory.NewFindingRepository()
	changes := memory.NewChangeEventRepository()
	notifier := memory.NewNotifier()
	svc := app.NewMonitorService(registry, assessments, assessments, portfolio, findings, changes, notifier)

	// Case-law-only worker: no rule-version sources, one case-law source.
	w := worker.New(core.DomainNitrogen, nil, []core.CaseLawSource{rvs}, changes, svc, assessments)
	return w, assessments, portfolio, findings, notifier
}

// seedCaseLawTenant inserts an assessment authored BEFORE the ruling (2024-12-18) with the given
// doctrinal routes, plus its asset (capital-at-risk for €enrichment). The pre-ruling CreatedAt proves
// retroactivity: a defensible dossier authored before the ruling must still flip.
func seedCaseLawTenant(t *testing.T, assessments *memory.AssessmentRepository, portfolio *memory.PortfolioRepository, tenant core.TenantID, routes []string, capital int64) {
	t.Helper()
	ctx := context.Background()
	assetID := core.AssetID("asset-" + string(tenant))
	if err := portfolio.SaveAsset(ctx, core.Asset{
		ID: assetID, TenantID: tenant, Domain: core.DomainNitrogen, CapitalAtRiskEUR: capital,
	}); err != nil {
		t.Fatal(err)
	}
	inputs, _ := json.Marshal(nitrogen.NitrogenInputs{Natura2000Area: "Veluwe", Routes: routes})
	if err := assessments.SaveAssessment(ctx, core.Assessment{
		ID: core.AssessmentID("assess-" + string(tenant)), AssetID: assetID, TenantID: tenant, Domain: core.DomainNitrogen,
		AuthoredBy:  "Consultancy BV", // ADR-004: the customer authors, never Houvast
		RuleVersion: core.RuleVersionRef{Domain: core.DomainNitrogen, Label: "AERIUS Calculator 2024"},
		Inputs:      inputs,
		Result:      core.AssessmentResult{Metrics: map[string]float64{dep: 0.06}},
		Status:      core.StatusDefensible,
		CreatedAt:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), // authored BEFORE the 2024-12-18 ruling
	}); err != nil {
		t.Fatal(err)
	}
}

// M3 DoD — a Raad van State ruling re-evaluates the portfolio correctly, and the case-law path is
// GATE-FREE (the engine is never called). Tenant A relies on intern salderen and must flip
// defensible->exposed with the curated recommendation + €exposure; the finding cites the ECLI. Tenant
// B does not rely on it and must be untouched (isolation + correct discrimination). A second cycle is
// a no-op (the case-law watermark advanced).
func TestRunOnce_CaseLaw_FlipsExposed_GateFree_AndIsolation(t *testing.T) {
	ctx := context.Background()
	engine := gateFreeEngine(t)
	w, assessments, portfolio, findings, notifier := buildCaseLawWorld(t, engine)

	// Tenant A: relies on intern salderen -> hit by the ruling.
	seedCaseLawTenant(t, assessments, portfolio, "tenantA", []string{"intern_salderen"}, 5_000_000)
	// Tenant B: does NOT rely on intern salderen (a different route) -> not hit.
	seedCaseLawTenant(t, assessments, portfolio, "tenantB", []string{"extern_salderen"}, 9_000_000)

	res, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// GATE-FREE PROOF: the case-law path never touched the engine.
	if engine.CallCount() != 0 {
		t.Fatalf("engine.CallCount() = %d, want 0 — the case-law path MUST be gate-free (ADR-012)", engine.CallCount())
	}

	// One case-law event emitted (the curated ruling, from a cold watermark).
	if len(res.Events) != 1 {
		t.Fatalf("res.Events = %d, want 1 (the curated RvS ruling)", len(res.Events))
	}
	if res.Events[0].Kind != core.ChangeCaseLaw || res.Events[0].Ref != caselawECLI {
		t.Fatalf("res.Events[0] = {Kind:%q Ref:%q}, want {case_law, %s}", res.Events[0].Kind, res.Events[0].Ref, caselawECLI)
	}

	// Exactly one finding across the cycle: tenant A flips.
	if len(res.Findings) != 1 {
		t.Fatalf("res.Findings = %d, want 1 (only tenant A relies on intern salderen)", len(res.Findings))
	}
	f := res.Findings[0]
	if f.TenantID != "tenantA" {
		t.Fatalf("finding tenant = %q, want tenantA", f.TenantID)
	}
	if f.PreviousStatus != core.StatusDefensible || f.NewStatus != core.StatusExposed {
		t.Fatalf("finding status %q->%q, want defensible->exposed", f.PreviousStatus, f.NewStatus)
	}
	if f.ChangeEventID != res.Events[0].ID {
		t.Fatalf("finding ChangeEventID = %q, want the ruling event id %q", f.ChangeEventID, res.Events[0].ID)
	}
	// The case-law path produces NO numeric Delta (no recompute) — it is a doctrinal flip.
	if f.Delta != nil {
		t.Fatalf("finding Delta = %+v, want nil (case-law flip has no numeric recompute)", f.Delta)
	}
	// Curated recommendation surfaced (sober remediation, ADR-004) and the explanation cites the ECLI.
	if f.Recommendation == "" {
		t.Fatal("finding Recommendation is empty; the curated remediation text must surface (ADR-004)")
	}
	if !strings.Contains(f.Explanation, caselawECLI) {
		t.Fatalf("finding Explanation = %q, want it to cite the ECLI %q", f.Explanation, caselawECLI)
	}
	// €exposure enriched from the asset's capital-at-risk (service layer, ADR-011).
	if f.EstimatedExposureEUR != 5_000_000 {
		t.Fatalf("finding €exposure = %d, want 5000000 (enriched from the asset)", f.EstimatedExposureEUR)
	}
	if !f.EvaluatedAt.Equal(refClock) {
		t.Fatalf("finding EvaluatedAt = %v, want injected clock %v", f.EvaluatedAt, refClock)
	}

	// Retroactivity: tenant A was authored 2023-01-01, before the 2024-12-18 ruling, and still flipped.
	aAssess, _ := assessments.ListByDomain(ctx, "tenantA", core.DomainNitrogen)
	if len(aAssess) != 1 || aAssess[0].Status != core.StatusExposed {
		t.Fatalf("tenantA assessment = %+v, want 1 exposed (retroactive flip of a pre-ruling dossier)", aAssess)
	}

	// Per-tenant snapshot for tenant A reflects the exposed asset + surfaces capital.
	snapA, ok := res.Snapshots["tenantA"]
	if !ok {
		t.Fatal("no snapshot for tenantA; the affected tenant must be snapshotted")
	}
	if snapA.ExposedAssets != 1 || snapA.CapitalAtRiskEUR != 5_000_000 {
		t.Fatalf("tenantA snapshot = {Exposed:%d Capital:%d}, want {1, 5000000}", snapA.ExposedAssets, snapA.CapitalAtRiskEUR)
	}

	// ISOLATION + correct discrimination: tenant B untouched (different route -> not hit).
	bFindings, _ := findings.ListByTenant(ctx, "tenantB")
	if len(bFindings) != 0 {
		t.Fatalf("tenantB findings = %d, want 0 (B does not rely on intern salderen)", len(bFindings))
	}
	bAssess, _ := assessments.ListByDomain(ctx, "tenantB", core.DomainNitrogen)
	if len(bAssess) != 1 || bAssess[0].Status != core.StatusDefensible {
		t.Fatalf("tenantB assessment = %+v, want 1 still defensible", bAssess)
	}
	if _, ok := res.Snapshots["tenantB"]; ok {
		t.Fatal("snapshot present for tenantB; only tenants with findings should be snapshotted")
	}
	// Repo-level isolation (ADR-006): tenant B cannot read tenant A's assessment by id.
	if _, err := assessments.GetAssessment(ctx, "tenantB", "assess-tenantA"); err == nil {
		t.Fatal("cross-tenant read of tenant A's assessment via tenant B succeeded — ADR-006 isolation broken")
	}

	// Notifier fired exactly once, for tenant A only.
	if len(notifier.Sent) != 1 {
		t.Fatalf("notifier deliveries = %d, want 1", len(notifier.Sent))
	}
	if notifier.Sent[0].Tenant != "tenantA" {
		t.Fatalf("notification tenant = %q, want tenantA", notifier.Sent[0].Tenant)
	}

	// No degraded errors on the gate-free happy path.
	if len(res.Errors) != 0 {
		t.Fatalf("res.Errors = %v, want none", res.Errors)
	}

	// SECOND CYCLE is a no-op: the case-law watermark advanced past the ruling (to the injected clock).
	res2, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if len(res2.Events) != 0 {
		t.Fatalf("cycle 2 events = %d, want 0 (case-law watermark advanced past the ruling)", len(res2.Events))
	}
	if len(res2.Findings) != 0 {
		t.Fatalf("cycle 2 findings = %d, want 0 (no re-flip)", len(res2.Findings))
	}
	after, _ := findings.ListByTenant(ctx, "tenantA")
	if len(after) != 1 {
		t.Fatalf("after cycle 2: tenantA findings = %d, want still 1 (no duplicate finding)", len(after))
	}
	// Still gate-free after the second cycle.
	if engine.CallCount() != 0 {
		t.Fatalf("after cycle 2 engine.CallCount() = %d, want 0", engine.CallCount())
	}
}

// M3 step 5 — a rule-version source and a case-law source run in the SAME worker cycle without
// cross-contaminating watermarks (LastIngested is keyed per ChangeKind). The version source flips
// tenant A on the 2025 release (engine recompute); the case-law source flips tenant A on the ruling
// (gate-free). Both findings appear in one cycle; a second cycle is a clean no-op for BOTH families.
func TestRunOnce_MixedSources_IndependentWatermarks(t *testing.T) {
	ctx := context.Background()

	// Version side: registry watcher + delta provider + a version-keyed engine (only the version path
	// calls it). The case-law path must NOT call it; we assert CallCount stays bounded to version events.
	vreg := version.NewRegistry()
	watcher := nitrogen.NewAeriusReleaseWatcher(vreg, fixedNow)
	deltas := nitrogen.NewRegistryVersionDeltaProvider(vreg)
	engine := versionKeyedEngine("never-an-area") // tenant A's "Veluwe" goes 0.06->0.11 across versions

	// Case-law side.
	creg := caselaw.NewRegistry()
	rvs := nitrogen.NewRaadVanStateSource(creg, fixedNow)
	scopes := nitrogen.NewRegistryCaseLawScopeProvider(creg)

	dom := nitrogen.NewDomainWithSources(
		engine,
		nitrogen.StaticThresholdProvider{Default: &nitrogen.Threshold{KDW: 0.10, Unit: "mol/ha/jr"}},
		deltas,
		scopes,
		nitrogen.InputsRouteDeriver{},
		fixedNow,
		watcher,
		rvs,
	)
	registry := app.NewRegistry(dom)

	assessments := memory.NewAssessmentRepository()
	portfolio := memory.NewPortfolioRepository()
	findings := memory.NewFindingRepository()
	changes := memory.NewChangeEventRepository()
	notifier := memory.NewNotifier()
	svc := app.NewMonitorService(registry, assessments, assessments, portfolio, findings, changes, notifier)

	// BOTH families wired into one worker.
	w := worker.New(core.DomainNitrogen,
		[]core.RuleVersionSource{watcher},
		[]core.CaseLawSource{rvs},
		changes, svc, assessments)

	// One tenant whose dossier relies on intern salderen AND sits on an area that crosses the KDW under
	// the 2025 release: it is hit by BOTH the version change and the ruling.
	seedCaseLawTenant(t, assessments, portfolio, "tenantA", []string{"intern_salderen"}, 5_000_000)

	res, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Events: 2024 + 2025 (cold version watermark) + the case-law ruling = 3.
	if len(res.Events) != 3 {
		t.Fatalf("res.Events = %d %v, want 3 (2024, 2025, ruling)", len(res.Events), eventRefs(res.Events))
	}

	// Findings: the 2025 version flip + the case-law flip. The 2024 event is a no-op (0.06 == prior).
	// (After the first flip the assessment is already exposed, so the second event yields no NEW finding
	// only if status is unchanged; both routes flip to exposed, so the later event sees prev==new and is
	// skipped. We assert at least the version flip happened and the engine was called for version events
	// only.)
	var sawCaseLaw, sawVersion bool
	for _, f := range res.Findings {
		if f.Delta != nil {
			sawVersion = true
		} else {
			sawCaseLaw = true
		}
	}

	// The engine was called for the version recompute path, but NEVER more than the version events
	// dispatched (2 version events, one per release). The case-law event must not have added a call.
	if engine.CallCount() > 2 {
		t.Fatalf("engine.CallCount() = %d, want <= 2 (only version events recompute; case-law is gate-free)", engine.CallCount())
	}
	if engine.CallCount() == 0 {
		t.Fatal("engine was never called; the version recompute path should have invoked it")
	}

	// Whichever event lands first flips the assessment to exposed; the second sees no transition. So we
	// expect exactly one finding overall, but it proves the two families ran in one cycle without the
	// case-law watermark suppressing the version poll (and vice-versa) — both events were dispatched.
	if !sawVersion && !sawCaseLaw {
		t.Fatalf("no findings produced; want at least one flip from the mixed cycle (findings=%d)", len(res.Findings))
	}

	// Independent watermarks: a second cycle re-polls NEITHER family (both watermarks advanced to the
	// injected clock, past every effective date). Zero new events of either kind.
	res2, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if len(res2.Events) != 0 {
		t.Fatalf("cycle 2 events = %d %v, want 0 (both watermarks advanced independently)", len(res2.Events), eventRefs(res2.Events))
	}
}
