// Package nitrogen is the FIRST vertical: a concrete implementation of the core domain ports for
// Dutch nitrogen (stikstof) / AERIUS permitting. All nitrogen-specific knowledge (mol/ha/jr, IMAER,
// AERIUS, Natura 2000, intern salderen) lives here — never in internal/core (ADR-007).
//
// STATUS: stubs. Methods panic("not implemented"). Flesh out per docs/ROADMAP.md (M1→M3).
package nitrogen

import (
	"context"
	"encoding/json"
	"errors"
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
//
// M2: this signature is unchanged on purpose (M1 callers/tests keep compiling). To expose the
// release watcher and RvS source, use the additive NewDomainWithSources below.
func NewDomain(engine core.CalculationEngine, thresholds ThresholdProvider, deltas VersionDeltaProvider, caselaw CaseLawScopeProvider, routes RouteDeriver, now func() time.Time) *Domain {
	return &Domain{
		engine:    engine,
		evaluator: NewImpactEvaluator(engine, thresholds, deltas, caselaw, routes, now),
	}
}

// NewDomainWithSources is the additive M2 constructor: it wires the same evaluator as NewDomain and
// ADDITIONALLY attaches the global RuleVersionSource (the AeriusReleaseWatcher) and CaseLawSource so
// that domain.RuleVersionSource() / domain.CaseLawSource() are non-nil for the worker. Pass a nil
// caselaw until M3; the worker only needs the version source in M2.
func NewDomainWithSources(engine core.CalculationEngine, thresholds ThresholdProvider, deltas VersionDeltaProvider, caselaw CaseLawScopeProvider, routes RouteDeriver, now func() time.Time, versions core.RuleVersionSource, caselawSource core.CaseLawSource) *Domain {
	d := NewDomain(engine, thresholds, deltas, caselaw, routes, now)
	d.versions = versions
	d.caselaw = caselawSource
	return d
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

// ErrConnectGated is returned by AeriusConnectEngine.Compute until the AERIUS Connect adapter is
// enabled. The real arms-length HTTP client (ADR-001) and IMAER GML marshalling are the GATED build,
// blocked on the commercial-terms validation gate (ADR-001/002; see docs/ROADMAP.md). Returning a
// clear sentinel — instead of panicking — lets the keep-alive worker degrade gracefully (ADR-002):
// a recompute error leaves the assessment's status UNTOUCHED (the M1 path in MonitorService.
// OnChangeEvent), never defaulting to defensible (ADR-004), instead of crashing the run.
var ErrConnectGated = errors.New("aerius connect adapter not yet enabled (commercial-terms gate, ADR-001/002)")

func (e *AeriusConnectEngine) Name() string { return "aerius-connect" }

// Compute is the single gated dependency (ADR-009): the authoritative RIVM AERIUS Connect recompute.
// Un-embedded by design — no real HTTP, no IMAER GML (that is the gated build, ADR-001). Until the
// gate clears it returns ErrConnectGated so callers degrade gracefully rather than crash.
func (e *AeriusConnectEngine) Compute(ctx context.Context, inputs json.RawMessage, version core.RuleVersionRef) (core.AssessmentResult, error) {
	// GATED (ADR-001/002): marshal NitrogenInputs -> IMAER GML -> Connect; persist result. Until the
	// commercial-terms gate clears, surface a sentinel instead of a panic so the worker degrades.
	return core.AssessmentResult{}, ErrConnectGated
}

// ---- AeriusReleaseWatcher : core.RuleVersionSource -------------------------
//
// Implemented in version_source.go (M2): a registry-backed watcher over the version-abstraction
// layer (ADR-003, see version/). It emits THIN ChangeEvent{Kind: ChangeRuleVersion} per release
// effective after `since`; assemble() looks up the curated delta + recomputes via Connect.

// ---- RaadVanStateSource : core.CaseLawSource -------------------------------
//
// Implemented in caselaw_source.go (M3): a registry-backed source over the case-law-abstraction layer
// (see caselaw/). It emits THIN ChangeEvent{Kind: ChangeCaseLaw} per curated ruling effective after a
// watermark; assemble() looks up the curated scope by Ref (the ECLI). The case-law path is gate-free —
// it never calls Connect (status flips from the route predicate + retroactivity in judge()).

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
