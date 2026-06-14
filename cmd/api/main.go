// Command api is the composition root for the Houvast HTTP API (M3 sub-slice a). It wires the full
// hexagon with in-memory infrastructure and the (gated) nitrogen domain, seeds a small dev portfolio,
// and serves the app services over JSON via internal/adapters/httpapi.
//
// This is one of the two places in the backend that may import every layer (core, app, the nitrogen
// domain + its version/caselaw layers, and the memory adapters): the composition root assembles the
// graph; the reusable httpapi adapter stays programmed to app+core ports. See docs/ARCHITECTURE.md.
//
// GATED ENGINE — graceful degradation (ADR-001/002): the AERIUS Connect engine is un-embedded; its
// Compute returns nitrogen.ErrConnectGated. So the VERSION change path detects the 2025 release but
// leaves statuses untouched, while the GATE-FREE case-law path (the curated RvS intern-salderen
// ruling) produces the real flip to EXPOSED. `go run ./cmd/api` starts WITHOUT panicking.
//
// AUTHN IS A STUB: the API resolves the tenant from the X-Tenant-ID header (httpapi.tenantMiddleware).
// That is a development stand-in for real authentication (JWT/OIDC), deferred (ADR-006/010/015).
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
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

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)
	ctx := context.Background()

	// Fixed clock so the dev seed + the demo ingest cycle are deterministic and reproducible. Picked
	// AFTER the curated 2025 release's mandatory effective date (2025-10-07) and the RvS ruling, so
	// POST /api/ingest detects both as new events. (A production server would use a real clock; this
	// is a dev composition root.)
	fixedNow := time.Date(2026, time.June, 13, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixedNow }

	// ---- In-memory infrastructure (Postgres deferred, ADR-010) --------------------------------
	assessments := memory.NewAssessmentRepository() // also implements app.TenantScope (ADR-011)
	portfolioRepo := memory.NewPortfolioRepository()
	findings := memory.NewFindingRepository()
	changes := memory.NewChangeEventRepository()
	notifier := memory.NewNotifier()

	// ---- Version layer + nitrogen domain (ADR-003/009) ----------------------------------------
	versionReg := version.NewRegistry() // curated 2024 + 2025 releases, 2024->2025 delta
	releaseWatcher := nitrogen.NewAeriusReleaseWatcher(versionReg, clock)
	deltaProvider := nitrogen.NewRegistryVersionDeltaProvider(versionReg)

	// ---- Case-law layer (M3, ADR-009) — the gate-free path ------------------------------------
	caselawReg := caselaw.NewRegistry() // curated ECLI:NL:RVS:2024:4923 (intern salderen weer vergunningplichtig)
	rvsSource := nitrogen.NewRaadVanStateSource(caselawReg, clock)
	caseLawScopes := nitrogen.NewRegistryCaseLawScopeProvider(caselawReg)

	// Global, single-config KDW thresholds (same for every tenant — ADR-009/010).
	thresholds := nitrogen.StaticThresholdProvider{
		ByArea: map[string]nitrogen.Threshold{
			"Veluwe":              {Area: "Veluwe", KDW: 0.10, Unit: "mol/ha/jr"},
			"Rijntakken":          {Area: "Rijntakken", KDW: 0.12, Unit: "mol/ha/jr"},
			"Naardermeer":         {Area: "Naardermeer", KDW: 0.08, Unit: "mol/ha/jr"},
			"Nieuwkoopse Plassen": {Area: "Nieuwkoopse Plassen", KDW: 0.09, Unit: "mol/ha/jr"},
		},
		Default: &nitrogen.Threshold{Area: "(default)", KDW: 0.10, Unit: "mol/ha/jr"},
	}

	// Real (gated) AERIUS Connect engine: Compute returns nitrogen.ErrConnectGated (ADR-001/002).
	engine := &nitrogen.AeriusConnectEngine{}

	// Wire the nitrogen domain WITH both sources AND the INDICATIVE Surface B checker (so POST /api/check
	// and /api/promote work). The version path is gated; the case-law path is gate-free.
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

	monitor := app.NewMonitorService(
		registry,
		assessments, // app.TenantScope (ADR-011)
		assessments,
		portfolioRepo,
		findings,
		changes,
		notifier,
	)

	check := app.NewCheckService(
		registry,
		portfolioRepo,
		assessments,
		clockFunc(clock),
		&counterIDs{prefix: "promoted"},
	)

	// CSV portfolio import (ADR-010 MVP cut). The nitrogen parser is injected here (the composition
	// root) so the app.ImportService — and the whole app layer — never imports the nitrogen package.
	// Imports are gate-free: they record the consultant's existing assessments (EngineRef="imported",
	// status defensible), then the monitor re-evaluates them on change events like any other.
	importer := app.NewImportService(
		nitrogen.NewPortfolioCSVParser(),
		portfolioRepo,
		assessments,
		clockFunc(clock),
	)

	// ---- DEV SEED ----------------------------------------------------------------------------------
	// A small dev portfolio so the API serves non-empty data: assessments across 2 tenants, each
	// AuthoredBy a CUSTOMER/CONSULTANT of record (ADR-004 — never Houvast), some relying on
	// intern_salderen so the curated RvS ruling flips them when POST /api/ingest runs. THIS IS A DEV
	// SEED ONLY: real portfolios arrive via the deferred ingestion connectors / import (ADR-010), with
	// AuthoredBy set at import/promote time.
	seedDevPortfolio(ctx, logger, portfolioRepo, assessments)

	// ---- The dev/admin ingest seam: the engine-agnostic worker behind httpapi.Ingester ------------
	w := worker.New(
		core.DomainNitrogen,
		[]core.RuleVersionSource{releaseWatcher},
		[]core.CaseLawSource{rvsSource},
		changes,
		monitor,
		assessments, // app.TenantScope
	)

	handler := httpapi.NewRouter(monitor, check, importer, w, logger)

	addr := ":" + port()
	logger.Printf("api: listening on %s (clock=%s, AERIUS Connect GATED — version path degrades, case-law path live)",
		addr, fixedNow.Format(time.RFC3339))
	logger.Printf("api: tenant resolved from %q header (DEV authn stub — real JWT/OIDC deferred, ADR-006/015)", "X-Tenant-ID")
	for _, route := range httpapi.Routes() {
		logger.Printf("api: route %s", route)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		logger.Fatalf("api: server stopped: %v", err)
	}
}

// port returns the listen port: $PORT if set, else 8080.
func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8080"
}

// clockFunc adapts a func() time.Time to core.Clock (the CheckService dependency).
type clockFunc func() time.Time

func (c clockFunc) Now() time.Time { return c() }

var _ core.Clock = clockFunc(nil)

// counterIDs is a simple, process-unique app.IDGenerator for promoted assets/assessments. Production
// would use UUIDs; an atomic counter keeps the dev server dependency-free (stdlib only) and unique.
type counterIDs struct {
	prefix string
	n      atomic.Int64
}

func (g *counterIDs) NewID() string {
	return g.prefix + "-" + strconv.FormatInt(g.n.Add(1), 10)
}

var _ app.IDGenerator = (*counterIDs)(nil)

// seedDevPortfolio inserts the dev portfolio. See the call site for the ADR-004/010 caveats. Mirrors
// the worker's seed (2 tenants; customers as AuthoredBy; computed under AERIUS 2024 so the 2025
// release is genuinely new; some relying on intern_salderen so the RvS ruling flips them).
func seedDevPortfolio(ctx context.Context, logger *log.Logger, portfolioRepo *memory.PortfolioRepository, assessments *memory.AssessmentRepository) {
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
		authoredBy string // a CUSTOMER/CONSULTANT (ADR-004) — never "Houvast"
		capital    int64
		metadata   map[string]string
		inputs     nitrogen.NitrogenInputs
		headline   string
		deposition float64
	}

	seeds := []seed{
		{
			tenant: "tenant-vandenberg", assetID: "asset-veluwe-noord", assessment: "assess-veluwe-noord-2024",
			name: "Woningbouw Veluwe-Noord", authoredBy: "Royal HaskoningDHV", capital: 4_200_000,
			metadata: map[string]string{"natura2000": "Veluwe", "homes": "180"},
			inputs:   nitrogen.NitrogenInputs{Natura2000Area: "Veluwe", DistanceKm: 2.1, Homes: 180, Routes: []string{"intern_salderen"}},
			headline: "0,06 mol/ha/jr op Veluwe", deposition: 0.06,
		},
		{
			tenant: "tenant-vandenberg", assetID: "asset-rijntakken-kade", assessment: "assess-rijntakken-kade-2024",
			name: "Kadeproject Rijntakken", authoredBy: "Royal HaskoningDHV", capital: 7_800_000,
			metadata: map[string]string{"natura2000": "Rijntakken", "m2": "12000"},
			inputs:   nitrogen.NitrogenInputs{Natura2000Area: "Rijntakken", DistanceKm: 0.8, CommercialM2: 12000, Routes: []string{"extern_salderen"}},
			headline: "0,09 mol/ha/jr op Rijntakken", deposition: 0.09,
		},
		{
			tenant: "tenant-meridiaan", assetID: "asset-naardermeer-park", assessment: "assess-naardermeer-park-2024",
			name: "Bedrijvenpark Naardermeer", authoredBy: "Arcadis Nederland", capital: 11_500_000,
			metadata: map[string]string{"natura2000": "Naardermeer", "m2": "26000"},
			inputs:   nitrogen.NitrogenInputs{Natura2000Area: "Naardermeer", DistanceKm: 1.4, CommercialM2: 26000, Routes: []string{"intern_salderen"}},
			headline: "0,07 mol/ha/jr op Naardermeer", deposition: 0.07,
		},
	}

	for _, s := range seeds {
		asset := core.Asset{
			ID:               s.assetID,
			TenantID:         s.tenant,
			Domain:           core.DomainNitrogen,
			Name:             s.name,
			Metadata:         s.metadata,
			CapitalAtRiskEUR: s.capital,
			CreatedAt:        authoredAt,
		}
		if err := portfolioRepo.SaveAsset(ctx, asset); err != nil {
			logger.Fatalf("seed: save asset %s: %v", s.assetID, err)
		}

		rawInputs, err := json.Marshal(s.inputs)
		if err != nil {
			logger.Fatalf("seed: marshal inputs for %s: %v", s.assessment, err)
		}

		assessment := core.Assessment{
			ID:              s.assessment,
			AssetID:         s.assetID,
			TenantID:        s.tenant,
			Domain:          core.DomainNitrogen,
			AuthoredBy:      s.authoredBy, // ADR-004: the customer/consultant is the author of record
			RuleVersion:     aerius2024,
			CaseLawBaseline: authoredAt,
			Inputs:          rawInputs,
			Result: core.AssessmentResult{
				Headline: s.headline,
				Metrics:  map[string]float64{"deposition_mol_ha_yr": s.deposition},
			},
			Status:    core.StatusDefensible, // defensible at authoring time
			CreatedAt: authoredAt,
		}
		if err := assessments.SaveAssessment(ctx, assessment); err != nil {
			logger.Fatalf("seed: save assessment %s: %v", s.assessment, err)
		}
	}

	logger.Printf("seed: %d dev assessments across 2 tenants (authored by customers per ADR-004, computed under %q)", len(seeds), aerius2024.Label)
}
