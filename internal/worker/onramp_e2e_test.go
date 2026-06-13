package worker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/houvast/houvast/internal/adapters/memory"
	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen"
	"github.com/houvast/houvast/internal/domains/nitrogen/caselaw"
	"github.com/houvast/houvast/internal/worker"
)

// onrampClock is the injected core.Clock for the CheckService (deterministic CreatedAt on promote).
type onrampClock struct{ t time.Time }

func (c onrampClock) Now() time.Time { return c.t }

// onrampIDs is a deterministic app.IDGenerator (asset then assessment per Promote call).
type onrampIDs struct{ n int }

func (g *onrampIDs) NewID() string { g.n++; return fmt.Sprintf("onramp-%d", g.n) }

// TestOnRamp_CheckPromoteThenWatched is the on-ramp's whole point — "check once, watched forever":
//
//  1. Surface B: Check a candidate site that relies on intern salderen, then Promote it into tenant
//     T's portfolio (authored by the customer, provisional/indicative — ADR-001/004).
//  2. Surface A: run the worker with the curated RvS case-law ruling (gate-free, no engine).
//  3. Assert the PROMOTED assessment flips defensible/attention → exposed, with the ECLI-cited finding.
//
// Both surfaces share the SAME tenant-scoped repos, so this proves a promoted project is genuinely
// monitored. The whole motion is deterministic: the checker is a heuristic, the case-law path needs
// no Connect (the engine fails the test if ever called).
func TestOnRamp_CheckPromoteThenWatched(t *testing.T) {
	ctx := context.Background()
	const tenant core.TenantID = "developerT"
	const customer = "Adviesbureau Stikstof BV"

	// One engine instance shared by both surfaces; it must NEVER be called (Check is gate-free,
	// the case-law path is gate-free). gateFreeEngine fails the test on any Compute call.
	engine := gateFreeEngine(t)

	// Shared infra.
	assessments := memory.NewAssessmentRepository()
	portfolio := memory.NewPortfolioRepository()
	findings := memory.NewFindingRepository()
	changes := memory.NewChangeEventRepository()
	notifier := memory.NewNotifier()

	// Case-law-only wiring (no rule-version source) WITH the indicative checker attached so the SAME
	// domain serves both Surface B (Check/Promote) and Surface A (OnChangeEvent).
	creg := caselaw.NewRegistry()
	rvs := nitrogen.NewRaadVanStateSource(creg, fixedNow)
	scopes := nitrogen.NewRegistryCaseLawScopeProvider(creg)

	dom := nitrogen.NewDomainWithSources(
		engine,
		nitrogen.StaticThresholdProvider{Default: &nitrogen.Threshold{KDW: 0.10, Unit: "mol/ha/jr"}},
		nitrogen.StaticVersionDeltaProvider{},
		scopes,
		nitrogen.InputsRouteDeriver{},
		fixedNow,
		nil, // no rule-version source: pure on-ramp + case-law motion
		rvs,
		nitrogen.WithLocationChecker(nitrogen.NewIndicativeChecker()),
	)
	registry := app.NewRegistry(dom)

	monitor := app.NewMonitorService(registry, assessments, assessments, portfolio, findings, changes, notifier)
	checkSvc := app.NewCheckService(registry, portfolio, assessments, onrampClock{refClock}, &onrampIDs{})
	w := worker.New(core.DomainNitrogen, nil, []core.CaseLawSource{rvs}, changes, monitor, assessments)

	// --- Surface B: Check, then Promote a site relying on intern salderen ---
	siteInputs, _ := json.Marshal(nitrogen.NitrogenInputs{
		Natura2000Area: "Veluwe",
		DistanceKm:     5, // far enough that the indicative verdict is not permit-required up front
		Homes:          40,
		BuildIntensity: 0.6,
		Routes:         []string{"intern_salderen"},
	})
	req := app.CheckRequest{Tenant: tenant, Domain: core.DomainNitrogen, Inputs: siteInputs}

	checkRes, err := checkSvc.Check(ctx, req)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Pre-monitoring it is NOT yet exposed (the ruling has not been applied). It is a buildable-ish
	// site that the customer chooses to monitor — the on-ramp's premise.
	if checkRes.Status == core.StatusExposed {
		t.Fatalf("Check status = exposed before any ruling; pick inputs that start non-exposed (got verdict %q)", checkRes.Verdict)
	}

	assetID, err := checkSvc.Promote(ctx, req, "Veluwe Noord fase 1", customer, checkRes.Result)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// The promoted assessment is in tenant T's portfolio, authored by the customer, provisional.
	promoted, err := assessments.ListByAsset(ctx, tenant, assetID)
	if err != nil || len(promoted) != 1 {
		t.Fatalf("ListByAsset(promoted) = %v, %v; want exactly 1", promoted, err)
	}
	pa := promoted[0]
	if pa.AuthoredBy != customer {
		t.Fatalf("promoted AuthoredBy = %q, want the customer %q (ADR-004)", pa.AuthoredBy, customer)
	}
	if pa.Result.EngineRef != "indicative" {
		t.Fatalf("promoted EngineRef = %q, want \"indicative\" (provisional, ADR-001)", pa.Result.EngineRef)
	}
	if pa.Status == core.StatusExposed {
		t.Fatalf("promoted status = exposed before the ruling; want non-exposed start")
	}
	statusBefore := pa.Status

	// --- Surface A: the curated RvS ruling lands and the worker fans it across the portfolio ---
	res, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Gate-free: the case-law path never recomputed via Connect.
	if engine.CallCount() != 0 {
		t.Fatalf("engine.CallCount() = %d, want 0 — the whole on-ramp+case-law motion is gate-free", engine.CallCount())
	}

	// Exactly one ruling event, exactly one finding — for the promoted assessment.
	if len(res.Events) != 1 || res.Events[0].Ref != caselawECLI {
		t.Fatalf("res.Events = %v, want the single ruling %s", eventRefs(res.Events), caselawECLI)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("res.Findings = %d, want 1 (the promoted assessment flips)", len(res.Findings))
	}
	f := res.Findings[0]
	if f.TenantID != tenant {
		t.Fatalf("finding tenant = %q, want %q", f.TenantID, tenant)
	}
	if f.AssessmentID != pa.ID {
		t.Fatalf("finding AssessmentID = %q, want the promoted assessment %q", f.AssessmentID, pa.ID)
	}
	if f.PreviousStatus != statusBefore || f.NewStatus != core.StatusExposed {
		t.Fatalf("finding status %q->%q, want %q->exposed", f.PreviousStatus, f.NewStatus, statusBefore)
	}
	// The case-law flip is doctrinal: no numeric recompute delta.
	if f.Delta != nil {
		t.Fatalf("finding Delta = %+v, want nil (case-law flip, no recompute)", f.Delta)
	}
	// The finding cites the ECLI and carries the curated remediation (ADR-004 sober recommendation).
	if !strings.Contains(f.Explanation, caselawECLI) {
		t.Fatalf("finding Explanation = %q, want it to cite %q", f.Explanation, caselawECLI)
	}
	if f.Recommendation == "" {
		t.Fatal("finding Recommendation is empty; the curated remediation must surface (ADR-004)")
	}
	if !f.EvaluatedAt.Equal(refClock) {
		t.Fatalf("finding EvaluatedAt = %v, want injected clock %v", f.EvaluatedAt, refClock)
	}

	// The PROMOTED assessment is now persisted as exposed — it is genuinely watched.
	after, _ := assessments.ListByAsset(ctx, tenant, assetID)
	if len(after) != 1 || after[0].Status != core.StatusExposed {
		t.Fatalf("promoted assessment after ruling = %+v, want exposed", after)
	}
	// And its authorship/provenance is untouched by the flip (still customer-authored, provisional).
	if after[0].AuthoredBy != customer {
		t.Fatalf("after flip AuthoredBy = %q, want still %q (a flip never rewrites authorship, ADR-004)", after[0].AuthoredBy, customer)
	}

	// Notifier fired once for tenant T.
	if len(notifier.Sent) != 1 || notifier.Sent[0].Tenant != tenant {
		t.Fatalf("notifier = %+v, want one delivery to %q", notifier.Sent, tenant)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("res.Errors = %v, want none", res.Errors)
	}
}
