package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/houvast/houvast/internal/adapters/memory"
	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen"
)

// --- deterministic injections -----------------------------------------------

// fixedClock is an injected core.Clock so Promote's CreatedAt is deterministic.
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// seqIDs is an injected app.IDGenerator that hands out deterministic, ordered IDs. Promote calls
// NewID twice (asset, then assessment), so the sequence proves which ID went where.
type seqIDs struct {
	prefix string
	n      int
}

func (g *seqIDs) NewID() string {
	g.n++
	return fmt.Sprintf("%s-%d", g.prefix, g.n)
}

// promoteClock is the injected timestamp every promoted asset/assessment must carry.
var promoteClock = time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

// newCheckWorld wires a CheckService over the nitrogen domain with the INDICATIVE checker attached
// (WithLocationChecker). It returns the service plus the tenant-scoped repos so a test can assert
// what was persisted — and, crucially, attempt a cross-tenant read.
func newCheckWorld(t *testing.T) (app.CheckService, *memory.PortfolioRepository, *memory.AssessmentRepository) {
	t.Helper()
	dom := nitrogen.NewDomain(
		nil, // no CalculationEngine: the Surface B path is gate-free (ADR-001) and must never touch Connect
		nitrogen.StaticThresholdProvider{Default: &nitrogen.Threshold{KDW: 0.10, Unit: "mol/ha/jr"}},
		nitrogen.StaticVersionDeltaProvider{},
		nitrogen.CuratedCaseLawScopeProvider{},
		nitrogen.InputsRouteDeriver{},
		func() time.Time { return promoteClock },
		nitrogen.WithLocationChecker(nitrogen.NewIndicativeChecker()),
	)
	registry := app.NewRegistry(dom)

	portfolio := memory.NewPortfolioRepository()
	assessments := memory.NewAssessmentRepository()
	svc := app.NewCheckService(registry, portfolio, assessments, fixedClock{promoteClock}, &seqIDs{prefix: "id"})
	return svc, portfolio, assessments
}

// inputsFor builds opaque nitrogen inputs for a candidate site.
func inputsFor(t *testing.T, in nitrogen.NitrogenInputs) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal inputs: %v", err)
	}
	return raw
}

// TestCheckService_Check proves the Surface B Check use-case maps the domain's CheckOutcome into a
// CheckResult — carrying Result, Verdict and Mitigations — and applies the verdict→status mapping:
// buildable→defensible, with_mitigation→attention, permit_required→exposed (ADR-001 UI signal only).
func TestCheckService_Check(t *testing.T) {
	svc, _, _ := newCheckWorld(t)

	cases := []struct {
		name            string
		inputs          nitrogen.NitrogenInputs
		wantVerdict     core.CheckVerdict
		wantStatus      core.DefensibilityStatus
		wantMitigations []string
	}{
		{
			name:            "buildable_maps_to_defensible",
			inputs:          nitrogen.NitrogenInputs{Natura2000Area: "Veluwe", DistanceKm: 25, Homes: 5, BuildIntensity: 0.5},
			wantVerdict:     core.VerdictBuildable,
			wantStatus:      core.StatusDefensible,
			wantMitigations: nil,
		},
		{
			name:            "with_mitigation_maps_to_attention",
			inputs:          nitrogen.NitrogenInputs{Natura2000Area: "Rijntakken", DistanceKm: 2, Homes: 200, BuildIntensity: 1},
			wantVerdict:     core.VerdictBuildableWithMitigation,
			wantStatus:      core.StatusAttention,
			wantMitigations: []string{"elektrisch bouwmaterieel", "fasering van de bouw"},
		},
		{
			name:            "permit_required_maps_to_exposed",
			inputs:          nitrogen.NitrogenInputs{Natura2000Area: "Veluwe", DistanceKm: 0.2, Homes: 800, CommercialM2: 50000, BuildIntensity: 2, Routes: []string{"intern_salderen"}},
			wantVerdict:     core.VerdictPermitRequired,
			wantStatus:      core.StatusExposed,
			wantMitigations: []string{"elektrisch bouwmaterieel", "fasering van de bouw", "intern of extern salderen", "emissiearme stalsystemen"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.Check(context.Background(), app.CheckRequest{
				Tenant: "tenantA",
				Domain: core.DomainNitrogen,
				Inputs: inputsFor(t, tc.inputs),
			})
			if err != nil {
				t.Fatalf("%s: Check: %v", tc.name, err)
			}
			if got.Verdict != tc.wantVerdict {
				t.Errorf("%s: Verdict = %q, want %q", tc.name, got.Verdict, tc.wantVerdict)
			}
			if got.Status != tc.wantStatus {
				t.Errorf("%s: Status = %q, want %q", tc.name, got.Status, tc.wantStatus)
			}
			if !reflect.DeepEqual(got.Mitigations, tc.wantMitigations) {
				t.Errorf("%s: Mitigations = %v, want %v", tc.name, got.Mitigations, tc.wantMitigations)
			}
			// ADR-001: the surfaced Result is indicative, never an official engine output.
			if got.Result.EngineRef != "" {
				t.Errorf("%s: Result.EngineRef = %q, want \"\" (Check is indicative, ADR-001)", tc.name, got.Result.EngineRef)
			}
		})
	}
}

// TestCheckService_Check_UnknownDomain proves Check errors (rather than silently succeeding) when no
// domain is registered for the request.
func TestCheckService_Check_UnknownDomain(t *testing.T) {
	svc, _, _ := newCheckWorld(t)
	if _, err := svc.Check(context.Background(), app.CheckRequest{Tenant: "tenantA", Domain: core.DomainKey("pfas")}); err == nil {
		t.Fatal("Check on an unregistered domain returned nil error; want a failure")
	}
}

// TestCheckService_Promote is the liability-critical test. Promoting a checked site:
//   - creates an Asset + Assessment persisted via the tenant-scoped repos,
//   - records AuthoredBy as the passed-in customer — NEVER Houvast (ADR-004),
//   - stamps the assessment's Result.EngineRef as the PROVISIONAL "indicative" sentinel, so it can
//     never be mistaken for a Connect-backed authoritative output (ADR-001),
//   - derives the status from the indicative verdict,
//   - uses the injected clock + IDGenerator (deterministic),
//   - and writes ONLY into req.Tenant — a different tenant cannot read the promoted artifacts (ADR-006).
func TestCheckService_Promote(t *testing.T) {
	ctx := context.Background()
	svc, portfolio, assessments := newCheckWorld(t)

	const customer = "Adviesbureau Stikstof BV"
	req := app.CheckRequest{
		Tenant: "tenantA",
		Domain: core.DomainNitrogen,
		// permit-required inputs → exposed status on promotion.
		Inputs: inputsFor(t, nitrogen.NitrogenInputs{
			Natura2000Area: "Veluwe", DistanceKm: 0.2, Homes: 800, CommercialM2: 50000,
			BuildIntensity: 2, Routes: []string{"intern_salderen"},
		}),
	}

	// The indicative Result a caller would have obtained from Check (carries no engine ref).
	indicativeResult := core.AssessmentResult{
		Headline: "Indicatieve schatting",
		Metrics:  map[string]float64{"deposition_mol_ha_yr": 0.8},
		// Deliberately empty: a real indicative result is not engine-backed.
	}

	assetID, err := svc.Promote(ctx, req, "Veluwe Noord fase 1", customer, indicativeResult)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// Deterministic IDs: first NewID → asset, second → assessment.
	if assetID != "id-1" {
		t.Fatalf("assetID = %q, want id-1 (first injected ID)", assetID)
	}

	// --- Asset persisted under tenant A ---
	asset, err := portfolio.GetAsset(ctx, "tenantA", assetID)
	if err != nil {
		t.Fatalf("GetAsset(tenantA): %v", err)
	}
	if asset.TenantID != "tenantA" || asset.Domain != core.DomainNitrogen || asset.Name != "Veluwe Noord fase 1" {
		t.Fatalf("asset = %+v, want tenantA/nitrogen/'Veluwe Noord fase 1'", asset)
	}
	if !asset.CreatedAt.Equal(promoteClock) {
		t.Fatalf("asset.CreatedAt = %v, want injected clock %v", asset.CreatedAt, promoteClock)
	}

	// --- Assessment persisted under tenant A ---
	all, err := assessments.ListByAsset(ctx, "tenantA", assetID)
	if err != nil {
		t.Fatalf("ListByAsset(tenantA): %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("assessments for asset = %d, want 1", len(all))
	}
	a := all[0]

	// ADR-004: the customer/consultant is the author of record — Houvast is NEVER the author.
	if a.AuthoredBy != customer {
		t.Errorf("AuthoredBy = %q, want the passed-in customer %q (ADR-004)", a.AuthoredBy, customer)
	}
	if a.AuthoredBy == "Houvast" || a.AuthoredBy == "houvast" {
		t.Errorf("AuthoredBy = %q — Houvast must NEVER be the author of an assessment (ADR-004)", a.AuthoredBy)
	}

	// ADR-001: the promoted assessment is PROVISIONAL — flagged "indicative", not Connect-backed. It
	// must NOT carry a ref that looks like an officially computed (RIVM) output.
	if a.Result.EngineRef != "indicative" {
		t.Errorf("Result.EngineRef = %q, want \"indicative\" (provisional, NOT Connect-backed, ADR-001)", a.Result.EngineRef)
	}

	// Status derived from the indicative verdict: permit-required inputs → exposed.
	if a.Status != core.StatusExposed {
		t.Errorf("Status = %q, want exposed (derived from the permit-required verdict)", a.Status)
	}

	// Identity / determinism: second injected ID, tenant + domain stamped, injected clock.
	if a.ID != "id-2" {
		t.Errorf("assessment ID = %q, want id-2 (second injected ID)", a.ID)
	}
	if a.TenantID != "tenantA" || a.Domain != core.DomainNitrogen || a.AssetID != assetID {
		t.Errorf("assessment scoping = {tenant:%q domain:%q asset:%q}, want tenantA/nitrogen/%q", a.TenantID, a.Domain, a.AssetID, assetID)
	}
	if !a.CreatedAt.Equal(promoteClock) {
		t.Errorf("assessment.CreatedAt = %v, want injected clock %v", a.CreatedAt, promoteClock)
	}

	// --- ADR-006 cross-tenant isolation on the promote path ---
	// A DIFFERENT tenant must not be able to read the promoted asset...
	if _, err := portfolio.GetAsset(ctx, "tenantB", assetID); err == nil {
		t.Fatal("tenantB read tenantA's promoted asset — ADR-006 isolation broken")
	}
	// ...nor its assessment by id...
	if _, err := assessments.GetAssessment(ctx, "tenantB", a.ID); err == nil {
		t.Fatal("tenantB read tenantA's promoted assessment by id — ADR-006 isolation broken")
	}
	// ...nor see it in any tenant-B listing.
	if got, _ := portfolio.ListAssets(ctx, "tenantB"); len(got) != 0 {
		t.Fatalf("tenantB ListAssets = %d, want 0 (promoted asset is tenantA-only, ADR-006)", len(got))
	}
	if got, _ := assessments.ListByDomain(ctx, "tenantB", core.DomainNitrogen); len(got) != 0 {
		t.Fatalf("tenantB ListByDomain = %d, want 0 (promoted assessment is tenantA-only, ADR-006)", len(got))
	}
}

// TestCheckService_Promote_RequiresAuthor proves the ADR-004 author guard: Promote refuses an empty
// authoredBy (it must never default to Houvast or persist an unauthored assessment), and writes nothing.
func TestCheckService_Promote_RequiresAuthor(t *testing.T) {
	ctx := context.Background()
	svc, portfolio, assessments := newCheckWorld(t)

	req := app.CheckRequest{
		Tenant: "tenantA",
		Domain: core.DomainNitrogen,
		Inputs: inputsFor(t, nitrogen.NitrogenInputs{Natura2000Area: "Veluwe", DistanceKm: 25, Homes: 5}),
	}
	if _, err := svc.Promote(ctx, req, "Site X", "", core.AssessmentResult{}); err == nil {
		t.Fatal("Promote with empty authoredBy returned nil error; ADR-004 requires a customer author")
	}
	// Nothing must have been persisted.
	if got, _ := portfolio.ListAssets(ctx, "tenantA"); len(got) != 0 {
		t.Fatalf("ListAssets after failed Promote = %d, want 0 (no partial write)", len(got))
	}
	if got, _ := assessments.ListByDomain(ctx, "tenantA", core.DomainNitrogen); len(got) != 0 {
		t.Fatalf("ListByDomain after failed Promote = %d, want 0 (no partial write)", len(got))
	}
}
