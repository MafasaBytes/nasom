package nitrogen

import (
	"context"
	"time"

	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen/caselaw"
)

// This file implements the M3 case-law-abstraction wiring for the nitrogen vertical:
//   - RaadVanStateSource (core.CaseLawSource) — registry-backed RvS ruling source.
//   - RegistryCaseLawScopeProvider (nitrogen.CaseLawScopeProvider) — the REAL-PATH provider that maps
//     a curated caselaw.Ruling to the evaluator's CaseLawScope, keyed by ECLI.
//
// Both read the global caselaw.Registry (the curated ruling->scope mapping, core IP — see
// docs/regulatory/intern-salderen-2024.md §8). The case-law path is GATE-FREE: it never calls AERIUS
// Connect. judge() flips status purely from the route predicate (NitrogenInputs.Routes contains the
// ruling's PredicateRoute) plus retroactivity — no physics, no recompute. The conservative analyst
// default (flag on the route; nuance in the recommendation) is preserved (note §6.2).
//
// The parent nitrogen package owns the caselaw->nitrogen mapping precisely so the caselaw/ package
// need not import nitrogen (which would create an import cycle via nitrogen.CaseLawScope).

// ---- RaadVanStateSource : core.CaseLawSource -------------------------------

// RaadVanStateSource ingests Raad van State rulings via the curated caselaw.Registry and emits THIN
// ChangeEvent{Kind: ChangeCaseLaw} for each ruling effective after a watermark. The event carries
// identity (Ref = ECLI) + the curated narrative summary; assemble() looks the machine-actionable scope
// up by Ref via the CaseLawScopeProvider (thin-event design, ADR-009).
//
// `now` is injected so IngestedAt is deterministic in tests; it defaults to time.Now.
type RaadVanStateSource struct {
	registry *caselaw.Registry
	now      func() time.Time
}

// NewRaadVanStateSource constructs the source over a registry. If registry is nil it falls back to the
// curated registry (caselaw.NewRegistry); if now is nil it defaults to time.Now.
func NewRaadVanStateSource(registry *caselaw.Registry, now func() time.Time) *RaadVanStateSource {
	if registry == nil {
		registry = caselaw.NewRegistry()
	}
	if now == nil {
		now = time.Now
	}
	return &RaadVanStateSource{registry: registry, now: now}
}

// Poll returns a thin ChangeEvent{Kind: ChangeCaseLaw} for each curated ruling whose ruling date is
// strictly after `since`. Events carry:
//   - Ref         = the ECLI (the identity assemble() looks up curated scope by — MUST equal the
//     scope-provider key),
//   - Summary     = the curated ruling narrative (human "what changed"),
//   - EffectiveAt = the ruling date,
//   - IngestedAt  = injected now() (deterministic in tests),
//   - Domain      = nitrogen.
//
// Rulings are emitted in ascending ruling-date order. The payload is intentionally empty: curated detail
// is looked up by Ref, not carried in the event (ADR-009 / note §5).
func (s *RaadVanStateSource) Poll(ctx context.Context, since time.Time) ([]core.ChangeEvent, error) {
	ingestedAt := s.now()
	var out []core.ChangeEvent
	for _, r := range s.registry.Since(since) { // ascending ruling-date order
		out = append(out, core.ChangeEvent{
			// The ECLI is a stable, unique identifier — use it as the event id so events don't
			// collide on a "" key in the repository and Findings can reliably reference their ruling.
			ID:          core.ChangeEventID(r.ECLI),
			Domain:      core.DomainNitrogen,
			Kind:        core.ChangeCaseLaw,
			Ref:         r.ECLI, // MUST equal the scope-provider key — assemble() looks up scope by Ref
			Summary:     r.Summary,
			EffectiveAt: r.RulingDate,
			IngestedAt:  ingestedAt,
		})
	}
	return out, nil
}

// ---- RegistryCaseLawScopeProvider : nitrogen.CaseLawScopeProvider ----------

// RegistryCaseLawScopeProvider is the REAL-PATH CaseLawScopeProvider: it maps a curated caselaw.Ruling
// from the registry to the evaluator's CaseLawScope, keyed by the change event's Ref (the ECLI). This
// is the boundary where the caselaw/ package's own Ruling type becomes nitrogen.CaseLawScope (keeping
// caselaw/ free of any nitrogen import). The CuratedCaseLawScopeProvider in providers.go remains for
// unit tests.
type RegistryCaseLawScopeProvider struct {
	registry *caselaw.Registry
}

// NewRegistryCaseLawScopeProvider constructs the provider over a registry. If registry is nil it falls
// back to the curated registry (caselaw.NewRegistry).
func NewRegistryCaseLawScopeProvider(registry *caselaw.Registry) RegistryCaseLawScopeProvider {
	if registry == nil {
		registry = caselaw.NewRegistry()
	}
	return RegistryCaseLawScopeProvider{registry: registry}
}

// Scope maps the curated ruling (looked up by e.Ref == ECLI) to the evaluator's CaseLawScope. It carries
// ONLY the five fields judge() needs (ECLI, PredicateRoute, EffectiveAt, Retroactive, Recommendation) —
// the conservative default (flag on the route; nuance in the recommendation) is in the curated data, not
// in code (note §6.2). An unknown ruling is an error (curation gap), never a silent defensible.
func (p RegistryCaseLawScopeProvider) Scope(ctx context.Context, e core.ChangeEvent) (CaseLawScope, error) {
	r, ok := p.registry.ByECLI(e.Ref)
	if !ok {
		return CaseLawScope{}, &unknownRulingError{ref: e.Ref}
	}
	return rulingToScope(r), nil
}

// rulingToScope converts a curated caselaw.Ruling to the evaluator's CaseLawScope. This is the single
// caselaw -> nitrogen mapping point.
func rulingToScope(r caselaw.Ruling) CaseLawScope {
	return CaseLawScope{
		ECLI:           r.ECLI,
		PredicateRoute: r.PredicateRoute,
		EffectiveAt:    r.RulingDate,
		Retroactive:    r.Retroactive,
		Recommendation: r.Recommendation,
	}
}

// unknownRulingError is returned by Scope when the event's Ref has no curated ruling (a curation gap).
type unknownRulingError struct{ ref string }

func (e *unknownRulingError) Error() string {
	return "nitrogen: no curated case-law scope for ruling " + e.ref
}

// CuratedScopesFromRegistry builds the ByRef map for a CuratedCaseLawScopeProvider from the curated
// registry. This lets the static (test-friendly) provider be populated from the same curated source
// as the registry-backed one, without duplicating the values.
func CuratedScopesFromRegistry(registry *caselaw.Registry) map[string]CaseLawScope {
	if registry == nil {
		registry = caselaw.NewRegistry()
	}
	out := map[string]CaseLawScope{}
	for _, r := range registry.All() {
		out[r.ECLI] = rulingToScope(r)
	}
	return out
}

// Compile-time assertions that the M3 adapters satisfy their ports.
var (
	_ core.CaseLawSource   = (*RaadVanStateSource)(nil)
	_ CaseLawScopeProvider = RegistryCaseLawScopeProvider{}
)
