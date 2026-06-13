package nitrogen

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/houvast/houvast/internal/core"
)

// metricDeposition is the nitrogen metric the evaluator judges on (mol N / ha / yr).
const metricDeposition = "deposition_mol_ha_yr"

// attentionBand: within this fraction of the KDW (but not over it) → "attention".
const attentionBand = 0.9

// ---- domain-private evaluation context (ADR-009) --------------------------
//
// evalContext is the typed bundle of everything judge() needs — and nothing it doesn't.
// It is assembled by assemble() (I/O) and consumed by judge() (pure). It NEVER appears in
// internal/core; if a second vertical ever needs generic rationale surfacing, that is a
// separate, additive promotion on core.Finding (see ADR-009), not this type.
type evalContext struct {
	ReferenceTime time.Time              // injected; judge() reads no clock
	PriorResult   core.AssessmentResult  // what the assessment claimed
	Recomputed    *core.AssessmentResult // engine recompute under the new version; nil for case-law
	Threshold     *Threshold             // KDW for the assessment's area; version path
	VersionDelta  *VersionDelta          // curated version diff; version path
	CaseLawScope  *CaseLawScope          // curated ruling scope; case-law path
	Route         Route                  // derived from the assessment's inputs; case-law path
}

// ---- reference / curated data shapes --------------------------------------

// Threshold is the critical deposition value (KDW) for a Natura 2000 area.
type Threshold struct {
	Area string
	KDW  float64
	Unit string
}

// VersionDelta describes what changed between two AERIUS releases (curated; ADR-003).
type VersionDelta struct {
	FromLabel string
	ToLabel   string
	Summary   string
}

// CaseLawScope is the machine-actionable scope of a ruling (curated core IP).
type CaseLawScope struct {
	ECLI           string
	PredicateRoute string // an assessment is "hit" if its Route relies on this route
	EffectiveAt    time.Time
	Retroactive    bool
	Recommendation string
}

// Route is the set of doctrinal routes an assessment relies on (e.g. intern_salderen).
type Route struct {
	ReliesOn map[string]bool
}

// ---- domain-internal provider ports ---------------------------------------
//
// These are wired ONCE from global config (ADR-009/010) — they are the same for every tenant
// (every tenant judges against the same AERIUS/KDW). They are NOT per-tenant configurable.

type ThresholdProvider interface {
	For(ctx context.Context, area string) (Threshold, error)
}

type VersionDeltaProvider interface {
	Between(ctx context.Context, from, to core.RuleVersionRef) (VersionDelta, error)
}

type CaseLawScopeProvider interface {
	Scope(ctx context.Context, e core.ChangeEvent) (CaseLawScope, error)
}

type RouteDeriver interface {
	Derive(inputs json.RawMessage) (Route, error)
}

// ---- NitrogenImpactEvaluator : core.ImpactEvaluator -----------------------
//
// The heart of the product. Per ADR-009 it splits into an I/O assemble() and a pure judge().
type NitrogenImpactEvaluator struct {
	engine     core.CalculationEngine
	thresholds ThresholdProvider
	deltas     VersionDeltaProvider
	caselaw    CaseLawScopeProvider
	routes     RouteDeriver
	now        func() time.Time // clock, read only inside assemble()
}

// NewImpactEvaluator wires the evaluator. now may be nil (defaults to time.Now).
func NewImpactEvaluator(engine core.CalculationEngine, thresholds ThresholdProvider, deltas VersionDeltaProvider, caselaw CaseLawScopeProvider, routes RouteDeriver, now func() time.Time) *NitrogenImpactEvaluator {
	if now == nil {
		now = time.Now
	}
	return &NitrogenImpactEvaluator{
		engine:     engine,
		thresholds: thresholds,
		deltas:     deltas,
		caselaw:    caselaw,
		routes:     routes,
		now:        now,
	}
}

func (ev *NitrogenImpactEvaluator) Evaluate(ctx context.Context, a core.Assessment, e core.ChangeEvent) (core.Finding, error) {
	ec, err := ev.assemble(ctx, a, e)
	if err != nil {
		return core.Finding{}, err
	}
	return judge(a, e, ec), nil
}

// assemble gathers context: engine recompute + provider lookups. All I/O lives here.
func (ev *NitrogenImpactEvaluator) assemble(ctx context.Context, a core.Assessment, e core.ChangeEvent) (evalContext, error) {
	ec := evalContext{ReferenceTime: ev.now(), PriorResult: a.Result}

	switch e.Kind {
	case core.ChangeRuleVersion:
		newVersion := core.RuleVersionRef{Domain: e.Domain, Label: e.Ref, EffectiveAt: e.EffectiveAt}
		res, err := ev.engine.Compute(ctx, a.Inputs, newVersion)
		if err != nil {
			return evalContext{}, fmt.Errorf("recompute under %s: %w", e.Ref, err)
		}
		ec.Recomputed = &res

		area := assessmentArea(a)
		th, err := ev.thresholds.For(ctx, area)
		if err != nil {
			return evalContext{}, fmt.Errorf("threshold for %q: %w", area, err)
		}
		ec.Threshold = &th

		delta, err := ev.deltas.Between(ctx, a.RuleVersion, newVersion)
		if err != nil {
			return evalContext{}, fmt.Errorf("version delta %s->%s: %w", a.RuleVersion.Label, e.Ref, err)
		}
		ec.VersionDelta = &delta

	case core.ChangeCaseLaw:
		scope, err := ev.caselaw.Scope(ctx, e)
		if err != nil {
			return evalContext{}, fmt.Errorf("caselaw scope for %s: %w", e.Ref, err)
		}
		ec.CaseLawScope = &scope

		route, err := ev.routes.Derive(a.Inputs)
		if err != nil {
			return evalContext{}, fmt.Errorf("derive route: %w", err)
		}
		ec.Route = route

	default:
		return evalContext{}, fmt.Errorf("unsupported change kind %q", e.Kind)
	}
	return ec, nil
}

// judge is PURE: a deterministic function of (assessment, change, context). No I/O, no clock,
// no error. This is the table-tested heart. €exposure is intentionally NOT set here — it needs
// the asset's capital-at-risk, which is orchestration context; MonitorService enriches it.
func judge(a core.Assessment, e core.ChangeEvent, ec evalContext) core.Finding {
	f := core.Finding{
		AssessmentID:   a.ID,
		ChangeEventID:  e.ID,
		TenantID:       a.TenantID,
		PreviousStatus: a.Status,
		NewStatus:      a.Status, // default: unchanged unless the logic below flips it
		EvaluatedAt:    ec.ReferenceTime,
	}

	switch e.Kind {
	case core.ChangeRuleVersion:
		oldVal := ec.PriorResult.Metrics[metricDeposition]
		newVal := ec.Recomputed.Metrics[metricDeposition]
		kdw := ec.Threshold.KDW
		f.Delta = &core.Delta{Metric: metricDeposition, Old: oldVal, New: newVal, Unit: ec.Threshold.Unit}

		switch {
		case newVal > kdw:
			f.NewStatus = core.StatusExposed
			f.Explanation = fmt.Sprintf(
				"Onder %s stijgt de depositie van %.2f naar %.2f %s en overschrijdt de KDW van %.2f voor %s.",
				e.Ref, oldVal, newVal, ec.Threshold.Unit, kdw, ec.Threshold.Area)
			f.Recommendation = "Herbereken en dien opnieuw in; overweeg aanvullende mitigatie."
		case newVal > kdw*attentionBand:
			f.NewStatus = core.StatusAttention
			f.Explanation = fmt.Sprintf(
				"Onder %s nadert de depositie (%.2f %s) de KDW van %.2f voor %s; controleer de marge.",
				e.Ref, newVal, ec.Threshold.Unit, kdw, ec.Threshold.Area)
			f.Recommendation = "Beoordeel of mitigatie nodig is voordat de uitspraak/versie definitief wordt."
		default:
			f.NewStatus = core.StatusDefensible
			f.Explanation = fmt.Sprintf("Onder %s blijft de depositie (%.2f %s) onder de KDW van %.2f.",
				e.Ref, newVal, ec.Threshold.Unit, kdw)
		}

	case core.ChangeCaseLaw:
		hit := ec.Route.ReliesOn[ec.CaseLawScope.PredicateRoute]
		applicable := ec.CaseLawScope.Retroactive || !a.CreatedAt.Before(ec.CaseLawScope.EffectiveAt)
		if hit && applicable {
			f.NewStatus = core.StatusExposed
			f.Explanation = fmt.Sprintf("Beroept zich op %q; %s (%s) maakt dit vergunningplichtig.",
				ec.CaseLawScope.PredicateRoute, e.Summary, ec.CaseLawScope.ECLI)
			f.Recommendation = ec.CaseLawScope.Recommendation
		} else {
			f.NewStatus = core.StatusDefensible
			f.Explanation = "Niet geraakt door deze uitspraak."
		}
	}

	return f
}

// assessmentArea extracts the Natura 2000 area from the assessment's nitrogen inputs.
func assessmentArea(a core.Assessment) string {
	var in NitrogenInputs
	_ = json.Unmarshal(a.Inputs, &in)
	return in.Natura2000Area
}
