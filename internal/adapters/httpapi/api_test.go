package httpapi_test

// HTTP API tests (M3 sub-slice a). These wire httpapi.NewRouter against the SAME graph cmd/api wires
// (in-memory infra + the gated nitrogen domain + version/caselaw layers + the worker), seed two
// tenants, and drive the endpoints through net/http/httptest.
//
// The load-bearing assertions are:
//   - the JSON CONTRACT: keys must match web/src/types/api.ts EXACTLY (camelCase, no snake_case),
//     because the frontend depends on them by name;
//   - TENANT ISOLATION (ADR-006): no header -> 401 and the service is never invoked un-scoped; a
//     tenant can never read another tenant's data via any endpoint (findings or portfolio);
//   - the ADR-004 author guard on promote (missing authoredBy -> 400, no asset created; author is the
//     customer, never "Houvast"; promoted result is provisional / indicative);
//   - graceful degradation (ADR-002): the gated version recompute path surfaces as collected errors,
//     never a 5xx / panic.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/houvast/houvast/internal/adapters/httpapi"
	"github.com/houvast/houvast/internal/adapters/memory"
	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen"
	"github.com/houvast/houvast/internal/domains/nitrogen/caselaw"
	"github.com/houvast/houvast/internal/domains/nitrogen/version"
	"github.com/houvast/houvast/internal/worker"
)

// ---- tenants used across the suite ----------------------------------------
const (
	tenantA = core.TenantID("tenant-vandenberg") // has intern_salderen + extern_salderen assets
	tenantB = core.TenantID("tenant-meridiaan")  // separate tenant — never sees A's data
)

// ---- deterministic doubles ------------------------------------------------

// fixedClock is a deterministic core.Clock for Promote.
type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// seqIDs is a deterministic app.IDGenerator: prefix-1, prefix-2, ...
type seqIDs struct {
	prefix string
	n      atomic.Int64
}

func (g *seqIDs) NewID() string { return g.prefix + "-" + strconv.FormatInt(g.n.Add(1), 10) }

// ---- harness --------------------------------------------------------------

// harness bundles the wired router plus the repos/spies the tests assert against.
type harness struct {
	router        http.Handler
	monitor       *spyMonitor // wraps the real MonitorService to detect un-scoped invocation (req 1)
	assess        *memory.AssessmentRepository
	portfolioRepo *memory.PortfolioRepository
	findings      *memory.FindingRepository
}

// spyMonitor wraps app.MonitorService and records whether any method was invoked, so the tenant
// middleware test can prove the service is NEVER reached on a missing-header request (ADR-006).
type spyMonitor struct {
	inner   app.MonitorService
	invoked atomic.Bool
}

func (s *spyMonitor) OnChangeEvent(ctx context.Context, e core.ChangeEvent) ([]core.Finding, error) {
	s.invoked.Store(true)
	return s.inner.OnChangeEvent(ctx, e)
}
func (s *spyMonitor) PortfolioExposure(ctx context.Context, t core.TenantID) (core.ExposureSnapshot, error) {
	s.invoked.Store(true)
	return s.inner.PortfolioExposure(ctx, t)
}
func (s *spyMonitor) Portfolio(ctx context.Context, t core.TenantID) ([]app.PortfolioProject, error) {
	s.invoked.Store(true)
	return s.inner.Portfolio(ctx, t)
}
func (s *spyMonitor) FindingsForAssessment(ctx context.Context, t core.TenantID, id core.AssessmentID) ([]core.Finding, error) {
	s.invoked.Store(true)
	return s.inner.FindingsForAssessment(ctx, t, id)
}

var _ app.MonitorService = (*spyMonitor)(nil)

// newHarness wires the full hexagon exactly as cmd/api does (in-memory infra + gated nitrogen domain
// + version/caselaw layers + worker), seeds two tenants, and returns the router and the repos the
// tests assert against. The clock is fixed AFTER the 2025 release (2025-10-07) and the RvS ruling so
// POST /api/ingest detects both as new events.
func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()

	fixedNow := time.Date(2026, time.June, 13, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixedNow }

	assessments := memory.NewAssessmentRepository() // also implements app.TenantScope (ADR-011)
	portfolioRepo := memory.NewPortfolioRepository()
	findings := memory.NewFindingRepository()
	changes := memory.NewChangeEventRepository()
	notifier := memory.NewNotifier()

	versionReg := version.NewRegistry()
	releaseWatcher := nitrogen.NewAeriusReleaseWatcher(versionReg, clock)
	deltaProvider := nitrogen.NewRegistryVersionDeltaProvider(versionReg)

	caselawReg := caselaw.NewRegistry()
	rvsSource := nitrogen.NewRaadVanStateSource(caselawReg, clock)
	caseLawScopes := nitrogen.NewRegistryCaseLawScopeProvider(caselawReg)

	thresholds := nitrogen.StaticThresholdProvider{
		ByArea: map[string]nitrogen.Threshold{
			"Veluwe":      {Area: "Veluwe", KDW: 0.10, Unit: "mol/ha/jr"},
			"Rijntakken":  {Area: "Rijntakken", KDW: 0.12, Unit: "mol/ha/jr"},
			"Naardermeer": {Area: "Naardermeer", KDW: 0.08, Unit: "mol/ha/jr"},
		},
		Default: &nitrogen.Threshold{Area: "(default)", KDW: 0.10, Unit: "mol/ha/jr"},
	}

	// Gated engine: Compute returns nitrogen.ErrConnectGated (ADR-001/002). The version path therefore
	// degrades gracefully; the case-law path is gate-free.
	engine := &nitrogen.AeriusConnectEngine{}

	domain := nitrogen.NewDomainWithSources(
		engine,
		thresholds,
		deltaProvider,
		caseLawScopes,
		nitrogen.InputsRouteDeriver{},
		clock,
		releaseWatcher,
		rvsSource,
		nitrogen.WithLocationChecker(nitrogen.NewIndicativeChecker()),
	)
	registry := app.NewRegistry(domain)

	realMonitor := app.NewMonitorService(registry, assessments, assessments, portfolioRepo, findings, changes, notifier)
	monitor := &spyMonitor{inner: realMonitor}

	check := app.NewCheckService(registry, portfolioRepo, assessments, fixedClock{fixedNow}, &seqIDs{prefix: "promoted"})

	w := worker.New(
		core.DomainNitrogen,
		[]core.RuleVersionSource{releaseWatcher},
		[]core.CaseLawSource{rvsSource},
		changes,
		realMonitor, // worker drives the real monitor (the spy only guards the HTTP edge)
		assessments,
	)

	seedTwoTenants(t, ctx, portfolioRepo, assessments, findings)

	return &harness{
		router:        httpapi.NewRouter(monitor, check, w, nil),
		monitor:       monitor,
		assess:        assessments,
		portfolioRepo: portfolioRepo,
		findings:      findings,
	}
}

// seedTwoTenants seeds a small two-tenant portfolio mirroring cmd/api's dev seed: assessments authored
// by CUSTOMERS (ADR-004 — never "Houvast"), computed under AERIUS 2024, some relying on intern_salderen
// so the curated RvS ruling flips them on ingest. Tenant A also gets a pre-existing finding so the
// cross-tenant findings test is meaningful (A sees it; B must see nothing for the same id).
func seedTwoTenants(t *testing.T, ctx context.Context, pr *memory.PortfolioRepository, ar *memory.AssessmentRepository, fr *memory.FindingRepository) {
	t.Helper()
	aerius2024 := core.RuleVersionRef{
		Domain:      core.DomainNitrogen,
		Label:       "AERIUS Calculator 2024",
		EffectiveAt: time.Date(2024, time.October, 1, 0, 0, 0, 0, time.UTC),
	}
	authoredAt := time.Date(2024, time.November, 15, 0, 0, 0, 0, time.UTC)

	type seed struct {
		tenant     core.TenantID
		assetID    core.AssetID
		assessment core.AssessmentID
		name       string
		authoredBy string
		capital    int64
		metadata   map[string]string
		inputs     nitrogen.NitrogenInputs
		headline   string
		deposition float64
	}
	seeds := []seed{
		{
			tenant: tenantA, assetID: "asset-veluwe-noord", assessment: "assess-veluwe-noord-2024",
			name: "Woningbouw Veluwe-Noord", authoredBy: "Royal HaskoningDHV", capital: 4_200_000,
			metadata: map[string]string{"natura2000": "Veluwe", "homes": "180"},
			inputs:   nitrogen.NitrogenInputs{Natura2000Area: "Veluwe", DistanceKm: 2.1, Homes: 180, Routes: []string{"intern_salderen"}},
			headline: "0,06 mol/ha/jr op Veluwe", deposition: 0.06,
		},
		{
			tenant: tenantA, assetID: "asset-rijntakken-kade", assessment: "assess-rijntakken-kade-2024",
			name: "Kadeproject Rijntakken", authoredBy: "Royal HaskoningDHV", capital: 7_800_000,
			metadata: map[string]string{"natura2000": "Rijntakken", "m2": "12000"},
			inputs:   nitrogen.NitrogenInputs{Natura2000Area: "Rijntakken", DistanceKm: 0.8, CommercialM2: 12000, Routes: []string{"extern_salderen"}},
			headline: "0,09 mol/ha/jr op Rijntakken", deposition: 0.09,
		},
		{
			tenant: tenantB, assetID: "asset-naardermeer-park", assessment: "assess-naardermeer-park-2024",
			name: "Bedrijvenpark Naardermeer", authoredBy: "Arcadis Nederland", capital: 11_500_000,
			metadata: map[string]string{"natura2000": "Naardermeer", "m2": "26000"},
			inputs:   nitrogen.NitrogenInputs{Natura2000Area: "Naardermeer", DistanceKm: 1.4, CommercialM2: 26000, Routes: []string{"intern_salderen"}},
			headline: "0,07 mol/ha/jr op Naardermeer", deposition: 0.07,
		},
	}

	for _, s := range seeds {
		asset := core.Asset{
			ID: s.assetID, TenantID: s.tenant, Domain: core.DomainNitrogen, Name: s.name,
			Metadata: s.metadata, CapitalAtRiskEUR: s.capital, CreatedAt: authoredAt,
		}
		if err := pr.SaveAsset(ctx, asset); err != nil {
			t.Fatalf("seed: save asset %s: %v", s.assetID, err)
		}
		raw, err := json.Marshal(s.inputs)
		if err != nil {
			t.Fatalf("seed: marshal inputs %s: %v", s.assessment, err)
		}
		assessment := core.Assessment{
			ID: s.assessment, AssetID: s.assetID, TenantID: s.tenant, Domain: core.DomainNitrogen,
			AuthoredBy: s.authoredBy, RuleVersion: aerius2024, CaseLawBaseline: authoredAt, Inputs: raw,
			Result: core.AssessmentResult{Headline: s.headline, Metrics: map[string]float64{"deposition_mol_ha_yr": s.deposition}},
			Status: core.StatusDefensible, CreatedAt: authoredAt,
		}
		if err := ar.SaveAssessment(ctx, assessment); err != nil {
			t.Fatalf("seed: save assessment %s: %v", s.assessment, err)
		}
	}

	// Pre-existing finding for tenant A's Veluwe assessment, so the findings endpoint has real data to
	// (a) return for A and (b) refuse to leak to B.
	if err := fr.Save(ctx, core.Finding{
		AssessmentID:         "assess-veluwe-noord-2024",
		ChangeEventID:        "evt-seed",
		TenantID:             tenantA,
		PreviousStatus:       core.StatusDefensible,
		NewStatus:            core.StatusAttention,
		Explanation:          "seed: prior attention finding for Veluwe-Noord",
		Recommendation:       "review under the new release",
		EstimatedExposureEUR: 4_200_000,
		EvaluatedAt:          time.Date(2025, time.October, 8, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed: save finding: %v", err)
	}
}

// ---- request helpers ------------------------------------------------------

// do issues a request with the given tenant header (empty header => no header set) and returns the
// recorder. A nil body sends no body.
func do(t *testing.T, h *harness, method, path string, tenant core.TenantID, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", string(tenant))
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

// decodeArray decodes the body into a slice of maps (so we can assert exact JSON keys).
func decodeArray(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode array: %v\nbody: %s", err, rec.Body.String())
	}
	return out
}

// decodeObject decodes the body into a map (so we can assert exact JSON keys).
func decodeObject(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode object: %v\nbody: %s", err, rec.Body.String())
	}
	return out
}

// assertKeys asserts the map has EXACTLY the want keys (presence + no extras), and that no key uses
// snake_case (the contract is camelCase — a snake_case key would silently break the frontend).
func assertKeys(t *testing.T, name string, got map[string]any, want []string) {
	t.Helper()
	wantSet := map[string]bool{}
	for _, k := range want {
		wantSet[k] = true
		if _, ok := got[k]; !ok {
			t.Errorf("%s: missing contract key %q (got keys %v)", name, k, keysOf(got))
		}
	}
	for k := range got {
		if !wantSet[k] {
			t.Errorf("%s: unexpected key %q not in api.ts contract (got keys %v)", name, k, keysOf(got))
		}
		if strings.ContainsRune(k, '_') {
			t.Errorf("%s: key %q is snake_case — breaks the camelCase api.ts contract", name, k)
		}
	}
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// =====================================================================================
// Req 1 — tenant middleware (ADR-006)
// =====================================================================================

func TestTenantMiddleware(t *testing.T) {
	// Every tenant-scoped route must 401 with no X-Tenant-ID, and the underlying service must NOT be
	// invoked un-scoped. /healthz must work without the header.
	scoped := []struct {
		name, method, path string
		body               any
	}{
		{"portfolio", http.MethodGet, "/api/portfolio", nil},
		{"exposure", http.MethodGet, "/api/portfolio/exposure", nil},
		{"findings", http.MethodGet, "/api/assessments/assess-veluwe-noord-2024/findings", nil},
		{"check", http.MethodPost, "/api/check", map[string]any{"domain": "nitrogen", "inputs": map[string]any{}}},
		{"promote", http.MethodPost, "/api/promote", map[string]any{"domain": "nitrogen", "authoredBy": "x", "name": "n"}},
		{"ingest", http.MethodPost, "/api/ingest", nil},
	}
	for _, tc := range scoped {
		t.Run("no_header_401_"+tc.name, func(t *testing.T) {
			h := newHarness(t)
			rec := do(t, h, tc.method, tc.path, "", tc.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401 with no X-Tenant-ID, got %d (body %s)", rec.Code, rec.Body.String())
			}
			// The service must never have run un-scoped (ADR-006: no code path runs un-scoped).
			if h.monitor.invoked.Load() {
				t.Errorf("monitor service was invoked despite missing tenant header — un-scoped access path exists")
			}
			// Error body uses the contract envelope {"error": ...}.
			obj := decodeObject(t, rec)
			if _, ok := obj["error"]; !ok {
				t.Errorf("401 body missing \"error\" envelope: %s", rec.Body.String())
			}
		})
	}

	t.Run("healthz_works_without_header", func(t *testing.T) {
		h := newHarness(t)
		rec := do(t, h, http.MethodGet, "/healthz", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200 for /healthz without header, got %d", rec.Code)
		}
		obj := decodeObject(t, rec)
		if obj["status"] != "ok" {
			t.Errorf("healthz body = %v, want status=ok", obj)
		}
	})
}

// =====================================================================================
// Req 2 — GET /api/portfolio
// =====================================================================================

func TestPortfolio(t *testing.T) {
	h := newHarness(t)
	rec := do(t, h, http.MethodGet, "/api/portfolio", tenantA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body %s)", rec.Code, rec.Body.String())
	}
	arr := decodeArray(t, rec)
	if len(arr) != 2 {
		t.Fatalf("tenant A portfolio: want 2 projects, got %d", len(arr))
	}

	for _, proj := range arr {
		// Contract: portfolioProjectDTO = { asset, latestAssessment, latestFinding }.
		assertKeys(t, "portfolioProject", proj, []string{"asset", "latestAssessment", "latestFinding"})

		asset, ok := proj["asset"].(map[string]any)
		if !ok {
			t.Fatalf("asset is not an object: %#v", proj["asset"])
		}
		assertKeys(t, "asset", asset, []string{"id", "tenantId", "domain", "name", "metadata", "capitalAtRiskEur", "createdAt"})

		// Tenant isolation: every asset belongs to tenant A only (req 2 + req 6).
		if asset["tenantId"] != string(tenantA) {
			t.Errorf("portfolio leaked a non-A asset: tenantId=%v", asset["tenantId"])
		}

		// latestAssessment present for seeded assets; assert its shape + the nested status/newStatus.
		la, ok := proj["latestAssessment"].(map[string]any)
		if !ok {
			t.Fatalf("latestAssessment missing/!object for %v: %#v", asset["name"], proj["latestAssessment"])
		}
		assertKeys(t, "assessment", la, []string{"id", "assetId", "tenantId", "domain", "authoredBy", "ruleVersionLabel", "result", "status", "createdAt"})
		if la["authoredBy"] == "Houvast" || la["authoredBy"] == "" {
			t.Errorf("ADR-004: assessment authoredBy must be the customer, got %q", la["authoredBy"])
		}
		if s, _ := la["status"].(string); s != "defensible" && s != "attention" && s != "exposed" {
			t.Errorf("assessment.status not a DefensibilityStatus: %q", s)
		}
		result, ok := la["result"].(map[string]any)
		if !ok {
			t.Fatalf("assessment.result missing/!object")
		}
		assertKeys(t, "assessmentResult", result, []string{"headline", "metrics", "engineRef"})
	}

	// The Veluwe project carries the seeded latestFinding -> assert the Finding contract incl. newStatus.
	var sawFinding bool
	for _, proj := range arr {
		lf, ok := proj["latestFinding"].(map[string]any)
		if !ok {
			continue // the other asset has no finding -> serializes to null
		}
		sawFinding = true
		assertKeys(t, "finding", lf, []string{
			"assessmentId", "changeEventId", "previousStatus", "newStatus",
			"explanation", "recommendation", "estimatedExposureEur", "evaluatedAt",
		}) // delta is omitempty -> absent here (nil)
		if lf["newStatus"] != "attention" {
			t.Errorf("seeded finding newStatus = %v, want attention", lf["newStatus"])
		}
	}
	if !sawFinding {
		t.Error("expected the Veluwe project to carry the seeded latestFinding, saw none")
	}
}

// =====================================================================================
// Req 3 — GET /api/portfolio/exposure
// =====================================================================================

func TestExposure(t *testing.T) {
	h := newHarness(t)
	rec := do(t, h, http.MethodGet, "/api/portfolio/exposure", tenantA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body %s)", rec.Code, rec.Body.String())
	}
	obj := decodeObject(t, rec)
	assertKeys(t, "exposureSnapshot", obj, []string{
		"tenantId", "totalAssets", "exposedAssets", "attentionAssets",
		"capitalPipelineEur", "capitalAtRiskEur", "generatedAt",
	})
	if obj["tenantId"] != string(tenantA) {
		t.Errorf("exposure tenantId = %v, want %s", obj["tenantId"], tenantA)
	}
	// Tenant A has 2 seeded assets; the pipeline must sum only A's capital (4.2M + 7.8M = 12M).
	if got := obj["totalAssets"]; got != float64(2) {
		t.Errorf("totalAssets = %v, want 2", got)
	}
	if got := obj["capitalPipelineEur"]; got != float64(12_000_000) {
		t.Errorf("capitalPipelineEur = %v, want 12000000 (only A's assets)", got)
	}
}

// =====================================================================================
// Req 4 — POST /api/check
// =====================================================================================

func TestCheck(t *testing.T) {
	cases := []struct {
		name        string
		inputs      map[string]any
		wantVerdict string
		wantStatus  string
	}{
		{
			// High homes + close distance -> deposition proxy >> 0.5 -> permit_required -> exposed.
			name:        "permit_required_maps_to_exposed",
			inputs:      map[string]any{"natura2000_area": "Veluwe", "distance_km": 0.1, "homes": 1000},
			wantVerdict: "permit_required",
			wantStatus:  "exposed",
		},
		{
			// Small project far away -> buildable -> defensible.
			name:        "buildable_maps_to_defensible",
			inputs:      map[string]any{"natura2000_area": "Veluwe", "distance_km": 50, "homes": 1},
			wantVerdict: "buildable",
			wantStatus:  "defensible",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			rec := do(t, h, http.MethodPost, "/api/check", tenantA, map[string]any{"domain": "nitrogen", "inputs": tc.inputs})
			if rec.Code != http.StatusOK {
				t.Fatalf("want 200, got %d (body %s)", rec.Code, rec.Body.String())
			}
			obj := decodeObject(t, rec)
			assertKeys(t, "checkResult", obj, []string{"result", "status", "verdict", "mitigations"})

			if obj["verdict"] != tc.wantVerdict {
				t.Errorf("verdict = %v, want %v", obj["verdict"], tc.wantVerdict)
			}
			if obj["status"] != tc.wantStatus {
				t.Errorf("status = %v, want %v", obj["status"], tc.wantStatus)
			}
			// mitigations must always be a JSON array (never null) — DTO normalizes nil -> [].
			if _, ok := obj["mitigations"].([]any); !ok {
				t.Errorf("mitigations must be a JSON array, got %#v", obj["mitigations"])
			}
			// ADR-001: an indicative pre-check makes NO Connect call -> engineRef is empty/indicative.
			result, ok := obj["result"].(map[string]any)
			if !ok {
				t.Fatalf("result missing/!object")
			}
			assertKeys(t, "assessmentResult", result, []string{"headline", "metrics", "engineRef"})
			if ref := result["engineRef"]; ref != "" {
				t.Errorf("ADR-001: check result engineRef must be empty (indicative, no Connect), got %q", ref)
			}
		})
	}
}

// =====================================================================================
// Req 5 — POST /api/promote
// =====================================================================================

func TestPromote(t *testing.T) {
	t.Run("creates_provisional_assessment_authored_by_customer", func(t *testing.T) {
		h := newHarness(t)
		body := map[string]any{
			"domain":     "nitrogen",
			"name":       "Nieuwbouw Testlocatie",
			"authoredBy": "Sweco Nederland",
			"inputs":     map[string]any{"natura2000_area": "Veluwe", "distance_km": 1.0, "homes": 50},
			"result":     map[string]any{"headline": "indicatief", "metrics": map[string]float64{"deposition_mol_ha_yr": 0.2}, "engineRef": ""},
		}
		rec := do(t, h, http.MethodPost, "/api/promote", tenantA, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("want 201, got %d (body %s)", rec.Code, rec.Body.String())
		}
		obj := decodeObject(t, rec)
		assertKeys(t, "promoteResponse", obj, []string{"assetId"})
		assetID := obj["assetId"].(string)
		if assetID == "" {
			t.Fatal("promote returned empty assetId")
		}

		// The promoted assessment must be authored by the customer (never Houvast) and provisional
		// (EngineRef == indicative), per ADR-004 / ADR-001. Read it back via the tenant-scoped repo.
		assessments, err := h.assess.ListByAsset(context.Background(), tenantA, core.AssetID(assetID))
		if err != nil {
			t.Fatalf("read back promoted assessment: %v", err)
		}
		if len(assessments) != 1 {
			t.Fatalf("want 1 promoted assessment, got %d", len(assessments))
		}
		a := assessments[0]
		if a.AuthoredBy != "Sweco Nederland" {
			t.Errorf("ADR-004: authoredBy = %q, want the customer (Sweco Nederland)", a.AuthoredBy)
		}
		if a.AuthoredBy == "Houvast" {
			t.Error("ADR-004 VIOLATION: assessment authored by Houvast")
		}
		if a.Result.EngineRef != "indicative" {
			t.Errorf("ADR-001: promoted result EngineRef = %q, want \"indicative\" (provisional)", a.Result.EngineRef)
		}
	})

	t.Run("missing_authoredBy_400_no_asset_created", func(t *testing.T) {
		h := newHarness(t)
		body := map[string]any{
			"domain": "nitrogen",
			"name":   "Geen Auteur",
			"inputs": map[string]any{"natura2000_area": "Veluwe", "distance_km": 1.0, "homes": 50},
			"result": map[string]any{"headline": "x", "metrics": map[string]float64{}, "engineRef": ""},
		}
		rec := do(t, h, http.MethodPost, "/api/promote", tenantA, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("ADR-004: missing authoredBy must be 400, got %d (body %s)", rec.Code, rec.Body.String())
		}
		obj := decodeObject(t, rec)
		if _, ok := obj["error"]; !ok {
			t.Errorf("400 body missing error envelope: %s", rec.Body.String())
		}
		// No asset must have been created (no partial write, ADR-004).
		assets, err := h.portfolioRepo.ListAssets(context.Background(), tenantA)
		if err != nil {
			t.Fatalf("list assets: %v", err)
		}
		// Only the 2 seeded assets — nothing new.
		if len(assets) != 2 {
			t.Errorf("missing-author promote created an asset: tenant A now has %d assets, want 2 (seed only)", len(assets))
		}
	})
}

// =====================================================================================
// Req 6 — GET /api/assessments/{id}/findings + cross-tenant denial (the load-bearing isolation test)
// =====================================================================================

func TestFindingsTenantIsolation(t *testing.T) {
	const veluweID = "assess-veluwe-noord-2024" // belongs to tenant A; has a seeded finding

	t.Run("tenantA_sees_own_findings", func(t *testing.T) {
		h := newHarness(t)
		rec := do(t, h, http.MethodGet, "/api/assessments/"+veluweID+"/findings", tenantA, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d (body %s)", rec.Code, rec.Body.String())
		}
		arr := decodeArray(t, rec)
		if len(arr) != 1 {
			t.Fatalf("tenant A: want 1 finding for its own assessment, got %d", len(arr))
		}
		assertKeys(t, "finding", arr[0], []string{
			"assessmentId", "changeEventId", "previousStatus", "newStatus",
			"explanation", "recommendation", "estimatedExposureEur", "evaluatedAt",
		})
		if arr[0]["assessmentId"] != veluweID {
			t.Errorf("finding assessmentId = %v, want %s", arr[0]["assessmentId"], veluweID)
		}
	})

	t.Run("tenantB_requesting_tenantA_assessment_gets_empty_never_A_findings", func(t *testing.T) {
		h := newHarness(t)
		// Tenant B asks for tenant A's assessment id. ADR-006: it must NEVER receive A's findings.
		rec := do(t, h, http.MethodGet, "/api/assessments/"+veluweID+"/findings", tenantB, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200 (empty), got %d (body %s)", rec.Code, rec.Body.String())
		}
		arr := decodeArray(t, rec)
		if len(arr) != 0 {
			t.Fatalf("ADR-006 CROSS-TENANT LEAK: tenant B received %d findings for tenant A's assessment %s", len(arr), veluweID)
		}
		// Defense in depth: the seeded A explanation must not appear anywhere in B's response body.
		if strings.Contains(rec.Body.String(), "Veluwe-Noord") {
			t.Errorf("ADR-006 CROSS-TENANT LEAK: tenant A's finding content surfaced in tenant B's response: %s", rec.Body.String())
		}
	})

	t.Run("tenantB_portfolio_never_contains_tenantA_projects", func(t *testing.T) {
		h := newHarness(t)
		rec := do(t, h, http.MethodGet, "/api/portfolio", tenantB, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", rec.Code)
		}
		arr := decodeArray(t, rec)
		if len(arr) != 1 {
			t.Fatalf("tenant B has exactly 1 seeded asset, got %d projects", len(arr))
		}
		for _, proj := range arr {
			asset := proj["asset"].(map[string]any)
			if asset["tenantId"] != string(tenantB) {
				t.Errorf("ADR-006 CROSS-TENANT LEAK: tenant B portfolio contains a %v asset", asset["tenantId"])
			}
		}
		// None of tenant A's asset ids may appear in B's portfolio body.
		for _, aid := range []string{"asset-veluwe-noord", "asset-rijntakken-kade"} {
			if strings.Contains(rec.Body.String(), aid) {
				t.Errorf("ADR-006 CROSS-TENANT LEAK: tenant A's asset %q appears in tenant B's portfolio", aid)
			}
		}
	})
}

// =====================================================================================
// Req 7 — POST /api/ingest: case-law flip + graceful version-path degradation
// =====================================================================================

func TestIngest(t *testing.T) {
	h := newHarness(t)
	rec := do(t, h, http.MethodPost, "/api/ingest", tenantA, nil)
	// Graceful degradation: the gated version recompute must NOT yield a 5xx or panic.
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest must degrade gracefully (no 5xx), got %d (body %s)", rec.Code, rec.Body.String())
	}
	obj := decodeObject(t, rec)
	assertKeys(t, "ingestResponse", obj, []string{"events", "findings", "snapshots", "errors"})

	// Both sources polled: the 2025 version release AND the RvS case-law ruling.
	events, _ := obj["events"].([]any)
	if len(events) < 2 {
		t.Fatalf("want >= 2 change events (version + case-law), got %d: %s", len(events), rec.Body.String())
	}

	// The gate-free case-law ruling must flip the intern_salderen assessments to exposed -> findings.
	findings, _ := obj["findings"].([]any)
	if len(findings) == 0 {
		t.Fatal("case-law ruling produced no findings — expected the intern_salderen assets to flip")
	}
	var sawExposed bool
	for _, f := range findings {
		fm := f.(map[string]any)
		assertKeys(t, "finding", fm, []string{
			"assessmentId", "changeEventId", "previousStatus", "newStatus",
			"explanation", "recommendation", "estimatedExposureEur", "evaluatedAt",
		})
		if fm["newStatus"] == "exposed" {
			sawExposed = true
		}
	}
	if !sawExposed {
		t.Error("expected at least one assessment to flip to exposed via the intern_salderen ruling")
	}

	// Graceful degradation (ADR-002): the gated version recompute surfaces as collected error strings,
	// not as a failure of the cycle. They must reference the gated Connect engine.
	errs, _ := obj["errors"].([]any)
	if len(errs) == 0 {
		t.Error("expected the gated version-recompute path to surface collected errors (graceful degradation)")
	}
	var sawGated bool
	for _, e := range errs {
		if s, ok := e.(string); ok && (strings.Contains(strings.ToLower(s), "gat") || strings.Contains(strings.ToLower(s), "connect")) {
			sawGated = true
		}
	}
	if !sawGated {
		t.Errorf("expected a gated-Connect error among collected errors, got %v", errs)
	}

	// After the flip, tenant A's intern_salderen assessment must report exposed via the read API, and
	// its findings endpoint must surface the ECLI-cited finding (gate-free path).
	frec := do(t, h, http.MethodGet, "/api/assessments/assess-veluwe-noord-2024/findings", tenantA, nil)
	if frec.Code != http.StatusOK {
		t.Fatalf("findings after ingest: want 200, got %d", frec.Code)
	}
	farr := decodeArray(t, frec)
	var sawECLI bool
	for _, f := range farr {
		if expl, _ := f["explanation"].(string); strings.Contains(expl, "ECLI") {
			sawECLI = true
		}
	}
	if !sawECLI {
		t.Errorf("expected an ECLI-cited case-law finding for the flipped assessment, got %v", farr)
	}
}
