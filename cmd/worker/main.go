// Command worker is the composition root for the Houvast keep-alive engine (M2). It wires the full
// hexagon with in-memory infrastructure and runs ONE ingest cycle, then logs what happened.
//
// This is the ONLY place in the backend that may import every layer (core, app, the nitrogen domain,
// the version + caselaw layers, and the memory adapters): the composition root assembles the graph; the
// reusable driver (internal/worker) stays engine-agnostic. See docs/ARCHITECTURE.md.
//
// TWO CHANGE PATHS (M3):
//   - VERSION PATH — GATED (ADR-001/002): the real AERIUS Connect engine is un-embedded and its Compute
//     returns nitrogen.ErrConnectGated until the commercial-terms gate clears. The 2025 release IS
//     detected, but every per-assessment recompute surfaces "Connect gated" and every status is left
//     UNTOUCHED (ADR-002/004) — graceful degradation, not a crash.
//   - CASE-LAW PATH — GATE-FREE (M3): the curated Raad van State ruling ECLI:NL:RVS:2024:4923 (intern
//     salderen weer vergunningplichtig) is detected and produces a REAL flip to EXPOSED for every
//     assessment relying on the `intern_salderen` route — no Connect, no recompute. judge() decides
//     from the route predicate + retroactivity alone.
//
// `go run ./cmd/worker` runs to completion WITHOUT panicking: it shows the real case-law flip(s) to
// exposed alongside the expected (logged, not fatal) gated-version degradation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"time"

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

	// Fixed injected clock so the run is deterministic. Picked AFTER the curated 2025 release's
	// mandatory effective date (2025-10-07) so the release watcher detects 2025 as a new event;
	// before that the cycle would see no change.
	fixedNow := time.Date(2026, time.June, 13, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixedNow }

	// ---- In-memory infrastructure (M1/M2; Postgres deferred, ADR-010) -------------------------
	assessments := memory.NewAssessmentRepository() // also implements app.TenantScope (ADR-011)
	portfolioRepo := memory.NewPortfolioRepository()
	findings := memory.NewFindingRepository()
	changes := memory.NewChangeEventRepository()
	notifier := memory.NewNotifier()

	// ---- Version layer + nitrogen domain (ADR-003/009) ----------------------------------------
	reg := version.NewRegistry()                                   // curated 2024 + 2025 releases, 2024->2025 delta
	releaseWatcher := nitrogen.NewAeriusReleaseWatcher(reg, clock) // core.RuleVersionSource (deterministic IngestedAt)
	deltaProvider := nitrogen.NewRegistryVersionDeltaProvider(reg) // real-path nitrogen.VersionDeltaProvider

	// ---- Case-law layer (M3, ADR-009) ---------------------------------------------------------
	// The curated Raad van State ruling->scope mapping (core IP — docs/regulatory/intern-salderen-2024.md).
	// The case-law path is GATE-FREE: rvsSource emits a thin ChangeCaseLaw event per ruling, and
	// caseLawScopes maps it to the curated CaseLawScope by ECLI. No Connect, no recompute — judge()
	// flips status from the route predicate (intern_salderen) + retroactivity alone.
	caselawReg := caselaw.NewRegistry()                                   // curated ECLI:NL:RVS:2024:4923
	rvsSource := nitrogen.NewRaadVanStateSource(caselawReg, clock)        // core.CaseLawSource (deterministic IngestedAt)
	caseLawScopes := nitrogen.NewRegistryCaseLawScopeProvider(caselawReg) // real-path nitrogen.CaseLawScopeProvider

	// Global, single-config KDW thresholds (same for every tenant — ADR-009/010). A permissive
	// default keeps the dev seed simple; real curated KDW data is a later swap behind this same port.
	thresholds := nitrogen.StaticThresholdProvider{
		ByArea: map[string]nitrogen.Threshold{
			"Veluwe":              {Area: "Veluwe", KDW: 0.10, Unit: "mol/ha/jr"},
			"Rijntakken":          {Area: "Rijntakken", KDW: 0.12, Unit: "mol/ha/jr"},
			"Naardermeer":         {Area: "Naardermeer", KDW: 0.08, Unit: "mol/ha/jr"},
			"Nieuwkoopse Plassen": {Area: "Nieuwkoopse Plassen", KDW: 0.09, Unit: "mol/ha/jr"},
		},
		Default: &nitrogen.Threshold{Area: "(default)", KDW: 0.10, Unit: "mol/ha/jr"},
	}

	// Real (gated) AERIUS Connect engine: un-embedded; Compute returns nitrogen.ErrConnectGated
	// (ADR-001/002). This is what makes the M2 run degrade gracefully instead of producing findings.
	engine := &nitrogen.AeriusConnectEngine{}

	// Wire the nitrogen domain WITH both sources (M3): the gated version path (releaseWatcher +
	// gated engine) AND the gate-free case-law path (rvsSource + caseLawScopes). domain.CaseLawSource()
	// is now non-nil so the worker drives the Raad van State source.
	domain := nitrogen.NewDomainWithSources(
		engine,
		thresholds,
		deltaProvider,
		caseLawScopes, // CaseLawScopeProvider (M3, registry-backed)
		nitrogen.InputsRouteDeriver{},
		clock,
		releaseWatcher,
		rvsSource, // core.CaseLawSource (M3)
	)

	registry := app.NewRegistry(domain)

	monitor := app.NewMonitorService(
		registry,
		assessments, // app.TenantScope (the assessment repo doubles as the tenant enumerator, ADR-011)
		assessments,
		portfolioRepo,
		findings,
		changes,
		notifier,
	)

	// ---- M2 DEV SEED -------------------------------------------------------------------------------
	// A small dev portfolio so the cycle has something to fan across: 2-3 assessments across 2
	// tenants, each AuthoredBy a CUSTOMER/CONSULTANT of record (ADR-004 — never Houvast), with a
	// defensible status, computed under "AERIUS 2024" so the 2025 release is a genuine new version
	// to re-evaluate against. THIS IS AN M2 DEV SEED ONLY: real portfolios arrive via the deferred
	// ingestion connectors / CSV import (ADR-010), with AuthoredBy set at import/promote time.
	seedDevPortfolio(ctx, logger, portfolioRepo, assessments)

	// ---- Build the engine-agnostic ingest driver and run one cycle --------------------------------
	w := worker.New(
		core.DomainNitrogen,
		[]core.RuleVersionSource{releaseWatcher},
		[]core.CaseLawSource{rvsSource}, // M3: the gate-free Raad van State case-law source
		changes,
		monitor,
		assessments, // app.TenantScope
	)

	logger.Printf("worker: running one ingest cycle (deterministic clock=%s)", fixedNow.Format(time.RFC3339))
	res, err := w.RunOnce(ctx)
	if err != nil {
		// A fatal cycle error (e.g. watermark read) — distinct from the collected per-event
		// degradation below. Even here we do NOT panic.
		logger.Fatalf("worker: ingest cycle failed: %v", err)
	}

	logResult(logger, res)
	logger.Printf("worker: cycle complete (no panic) — gated-engine errors above are expected graceful degradation (ADR-002)")
}

// seedDevPortfolio inserts the M2 dev portfolio. See the call site for the ADR-004/010 caveats.
func seedDevPortfolio(ctx context.Context, logger *log.Logger, portfolioRepo *memory.PortfolioRepository, assessments *memory.AssessmentRepository) {
	aerius2024 := core.RuleVersionRef{
		Domain:      core.DomainNitrogen,
		Label:       "AERIUS Calculator 2024",
		EffectiveAt: time.Date(2024, time.October, 1, 0, 0, 0, 0, time.UTC),
	}
	authoredAt := time.Date(2024, time.November, 15, 0, 0, 0, 0, time.UTC) // before the 2025 release

	type seed struct {
		tenant     core.TenantID
		assetID    core.AssetID
		assessment core.AssessmentID
		name       string
		authoredBy string // a CUSTOMER/CONSULTANT (ADR-004) — never "Houvast"
		capital    int64
		inputs     nitrogen.NitrogenInputs
		headline   string
		deposition float64
	}

	seeds := []seed{
		{
			tenant: "tenant-vandenberg", assetID: "asset-veluwe-noord", assessment: "assess-veluwe-noord-2024",
			name: "Woningbouw Veluwe-Noord", authoredBy: "Royal HaskoningDHV", capital: 4_200_000,
			inputs:   nitrogen.NitrogenInputs{Natura2000Area: "Veluwe", DistanceKm: 2.1, Homes: 180, Routes: []string{"intern_salderen"}},
			headline: "0,06 mol/ha/jr op Veluwe", deposition: 0.06,
		},
		{
			tenant: "tenant-vandenberg", assetID: "asset-rijntakken-kade", assessment: "assess-rijntakken-kade-2024",
			name: "Kadeproject Rijntakken", authoredBy: "Royal HaskoningDHV", capital: 7_800_000,
			inputs:   nitrogen.NitrogenInputs{Natura2000Area: "Rijntakken", DistanceKm: 0.8, CommercialM2: 12000, Routes: []string{"extern_salderen"}},
			headline: "0,09 mol/ha/jr op Rijntakken", deposition: 0.09,
		},
		{
			tenant: "tenant-meridiaan", assetID: "asset-naardermeer-park", assessment: "assess-naardermeer-park-2024",
			name: "Bedrijvenpark Naardermeer", authoredBy: "Arcadis Nederland", capital: 11_500_000,
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
			RuleVersion:     aerius2024,   // computed under AERIUS 2024 → 2025 release is a new version
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

	logger.Printf("seed: %d M2 dev assessments across 2 tenants (authored by customers per ADR-004, computed under %q)", len(seeds), aerius2024.Label)
}

// logResult prints the cycle outcome: detected release event(s), findings, per-tenant exposure
// snapshots, and any collected errors (the gated-engine "Connect gated" errors are EXPECTED).
func logResult(logger *log.Logger, res worker.Result) {
	logger.Printf("=== detected change events (%d) ===", len(res.Events))
	for _, e := range res.Events {
		logger.Printf("  event: kind=%s ref=%q effective=%s summary=%q",
			e.Kind, e.Ref, e.EffectiveAt.Format("2006-01-02"), truncate(e.Summary, 80))
	}

	logger.Printf("=== findings (%d) ===", len(res.Findings))
	if len(res.Findings) == 0 {
		logger.Printf("  (none — expected: the gated AERIUS Connect recompute could not run, so no status transitioned; ADR-002/004)")
	}
	for _, f := range res.Findings {
		logger.Printf("  finding: tenant=%s assessment=%s %s->%s €%d %q",
			f.TenantID, f.AssessmentID, f.PreviousStatus, f.NewStatus, f.EstimatedExposureEUR, f.Explanation)
	}

	logger.Printf("=== exposure snapshots (%d tenants with findings) ===", len(res.Snapshots))
	for t, snap := range res.Snapshots {
		logger.Printf("  tenant=%s total=%d exposed=%d attention=%d pipeline=€%d at-risk=€%d",
			t, snap.TotalAssets, snap.ExposedAssets, snap.AttentionAssets, snap.CapitalPipelineEUR, snap.CapitalAtRiskEUR)
	}

	logger.Printf("=== collected errors (%d) ===", len(res.Errors))
	for _, e := range res.Errors {
		if errors.Is(e, nitrogen.ErrConnectGated) {
			logger.Printf("  EXPECTED graceful degradation (ADR-002): %v", e)
			continue
		}
		logger.Printf("  error: %v", e)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
