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
	Natura2000Area   string  `json:"natura2000_area"`
	DistanceKm       float64 `json:"distance_km"`
	Homes            int     `json:"homes"`
	CommercialM2     int     `json:"commercial_m2"`
	BuildIntensity   float64 `json:"build_intensity"`
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

// New wires the nitrogen adapters together. The Connect base URL / API key come from config (cmd/).
func New( /* cfg Config */ ) *Domain {
	panic("not implemented") // see docs/ROADMAP.md M1
}

func (d *Domain) Key() core.DomainKey                        { return core.DomainNitrogen }
func (d *Domain) CalculationEngine() core.CalculationEngine  { return d.engine }
func (d *Domain) RuleVersionSource() core.RuleVersionSource  { return d.versions }
func (d *Domain) CaseLawSource() core.CaseLawSource          { return d.caselaw }
func (d *Domain) ImpactEvaluator() core.ImpactEvaluator      { return d.evaluator }

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

// ---- NitrogenImpactEvaluator : core.ImpactEvaluator ------------------------
//
// The heart of the product for nitrogen. For a rule-version change: recompute via the engine and
// compare against the assessment's prior result/threshold -> Delta + status. For case law: match the
// ruling scope (e.g. "relies on intern salderen") against the assessment's route -> status + action.
type NitrogenImpactEvaluator struct {
	engine core.CalculationEngine
	// version mappings, threshold rules, caselaw scope matchers
}

func (ev *NitrogenImpactEvaluator) Evaluate(ctx context.Context, a core.Assessment, e core.ChangeEvent) (core.Finding, error) {
	panic("not implemented") // M1 (version path) / M3 (case-law path)
}

// Compile-time assertions that the adapters satisfy the core ports.
var (
	_ core.Domain            = (*Domain)(nil)
	_ core.CalculationEngine = (*AeriusConnectEngine)(nil)
	_ core.RuleVersionSource = (*AeriusReleaseWatcher)(nil)
	_ core.CaseLawSource     = (*RaadVanStateSource)(nil)
	_ core.ImpactEvaluator   = (*NitrogenImpactEvaluator)(nil)
)
