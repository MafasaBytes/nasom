// Package nitrogen is the FIRST vertical: a concrete implementation of the core domain ports for
// Dutch nitrogen (stikstof) / AERIUS permitting. All nitrogen-specific knowledge (mol/ha/jr, IMAER,
// AERIUS, Natura 2000, intern salderen) lives here — never in internal/core (ADR-007).
//
// STATUS: stubs. Methods panic("not implemented"). Flesh out per docs/ROADMAP.md (M1→M3).
package nitrogen

import (
	"context"
	"encoding/json"
	"time"

	"github.com/houvast/houvast/internal/core"
)

// NitrogenInputs is the domain-specific input shape carried opaquely through core as json.RawMessage.
// (Documented here so the schema lives with the domain.)
type NitrogenInputs struct {
	Natura2000Area string  `json:"natura2000_area"`
	DistanceKm     float64 `json:"distance_km"`
	Homes          int     `json:"homes"`
	CommercialM2   int     `json:"commercial_m2"`
	BuildIntensity float64 `json:"build_intensity"`
	// Routes are the doctrinal offsetting routes this assessment relies on (e.g. "intern_salderen").
	// Case-law evaluation matches a ruling's scope against these.
	Routes []string `json:"routes,omitempty"`
	// ... emission sources, coordinates, source heights, transport movements, etc.
	// For the official path these are serialized into IMAER GML for AERIUS Connect.
}

// Domain bundles the nitrogen adapters. Satisfies core.Domain.
type Domain struct {
	engine    core.CalculationEngine
	versions  core.RuleVersionSource
	caselaw   core.CaseLawSource
	evaluator core.ImpactEvaluator
}

// New wires the nitrogen adapters together from real config (Connect base URL / API key, curated
// dataset). Deferred until the Connect commercial-terms gate clears (ADR-001/002); it will build the
// real engine + providers and delegate to NewDomain.
func New( /* cfg Config */ ) *Domain {
	panic("not implemented") // see docs/ROADMAP.md M1 + the Connect gate
}

// NewDomain wires the nitrogen domain by dependency injection — used by the M1 motion and tests.
// The engine is injected (a deterministic fake in tests; the real arms-length AeriusConnectEngine
// once the gate clears). The reference providers are globally configured (ADR-009/010). The release
// watcher and RvS source are not wired in M1 — OnChangeEvent only needs the evaluator (M2/M3).
func NewDomain(engine core.CalculationEngine, thresholds ThresholdProvider, deltas VersionDeltaProvider, caselaw CaseLawScopeProvider, routes RouteDeriver, now func() time.Time) *Domain {
	return &Domain{
		engine:    engine,
		evaluator: NewImpactEvaluator(engine, thresholds, deltas, caselaw, routes, now),
	}
}

func (d *Domain) Key() core.DomainKey                       { return core.DomainNitrogen }
func (d *Domain) CalculationEngine() core.CalculationEngine { return d.engine }
func (d *Domain) RuleVersionSource() core.RuleVersionSource { return d.versions }
func (d *Domain) CaseLawSource() core.CaseLawSource         { return d.caselaw }
func (d *Domain) ImpactEvaluator() core.ImpactEvaluator     { return d.evaluator }

// ---- AeriusConnectEngine : core.CalculationEngine --------------------------
//
// Arms-length HTTP client to the official RIVM AERIUS Connect API (ADR-001). MUST NOT embed the
// AERIUS engine (AGPLv3). Persists the authoritative result immediately (Connect expires ~3 days,
// no SLA — ADR-002). Build IMAER GML from NitrogenInputs, submit, poll, retrieve, store.
type AeriusConnectEngine struct {
	baseURL string
	apiKey  string
	// httpClient, resultStore, ...
}

func (e *AeriusConnectEngine) Name() string { return "aerius-connect" }

func (e *AeriusConnectEngine) Compute(ctx context.Context, inputs json.RawMessage, version core.RuleVersionRef) (core.AssessmentResult, error) {
	panic("not implemented") // M1: marshal NitrogenInputs -> IMAER GML -> Connect; persist result
}

// ---- AeriusReleaseWatcher : core.RuleVersionSource -------------------------
//
// Watches the (annual) AERIUS release. Emits ChangeEvent{Kind: ChangeRuleVersion} carrying the
// version delta. Pair with the version-abstraction layer (ADR-003, see version/).
type AeriusReleaseWatcher struct{}

func (w *AeriusReleaseWatcher) Current(ctx context.Context) (core.RuleVersionRef, error) {
	panic("not implemented") // M2
}
func (w *AeriusReleaseWatcher) Poll(ctx context.Context, since time.Time) ([]core.ChangeEvent, error) {
	panic("not implemented") // M2
}

// ---- RaadVanStateSource : core.CaseLawSource -------------------------------
//
// Ingests Raad van State rulings + a curated mapping of doctrine changes to machine-actionable scope
// (start with intern salderen, 18 Dec 2024). The mapping is core IP — see DECISIONS open questions.
type RaadVanStateSource struct{}

func (s *RaadVanStateSource) Poll(ctx context.Context, since time.Time) ([]core.ChangeEvent, error) {
	panic("not implemented") // M3
}

// NitrogenImpactEvaluator (core.ImpactEvaluator) — the heart of the product — lives in evaluator.go,
// split into a pure judge() and an I/O assemble() per ADR-009.

// Compile-time assertions that the adapters satisfy the core ports.
var (
	_ core.Domain            = (*Domain)(nil)
	_ core.CalculationEngine = (*AeriusConnectEngine)(nil)
	_ core.RuleVersionSource = (*AeriusReleaseWatcher)(nil)
	_ core.CaseLawSource     = (*RaadVanStateSource)(nil)
	_ core.ImpactEvaluator   = (*NitrogenImpactEvaluator)(nil)
)
