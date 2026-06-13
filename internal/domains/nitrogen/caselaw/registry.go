// Package caselaw is the first-class case-law-abstraction layer for the nitrogen vertical (M3,
// mirrors version/ for ADR-003/009). It isolates ALL knowledge about which Raad van State rulings
// change nitrogen doctrine — the ruling identity (ECLI), the doctrinal route it scopes, the
// effective date, whether it reaches existing dossiers (retroactive), the curated human summary, and
// the sober remediation recommendation (ADR-004) — so that ingesting a new ruling is a contained,
// reviewable, cited migration. Like the version layer, this IS the keep-alive product (the curated
// ruling->scope mapping is the core IP, see docs/regulatory/intern-salderen-2024.md §8), not plumbing.
//
// Hard boundaries (do not cross):
//   - This package imports ONLY the standard library + internal/core. It deliberately does NOT import
//     the parent nitrogen package: nitrogen.CaseLawScope lives there and importing nitrogen here would
//     create an import cycle. caselaw owns its OWN Ruling type; the parent maps Ruling ->
//     nitrogen.CaseLawScope (see ../caselaw_source.go RegistryCaseLawScopeProvider).
//   - NO judgement logic. This layer carries curated, cited data and lookups only. Whether a given
//     assessment is "hit" by a ruling is decided in the evaluator's pure judge() (route predicate +
//     retroactivity), never here.
//
// All curated values trace to the regulatory-caselaw-analyst's cited note
// docs/regulatory/intern-salderen-2024.md (curated 2026-06-13). Citations are inline in curated.go.
package caselaw

import "time"

// Ruling is the curated, machine-actionable record of one nitrogen-relevant court ruling. It is this
// package's OWN type (no nitrogen import — see the package doc). The parent nitrogen package maps it
// to nitrogen.CaseLawScope for the evaluator.
//
// The five fields the evaluator's judge() needs (ECLI, PredicateRoute, EffectiveAt, Retroactive,
// Recommendation) suffice for this ruling — no shape change (see docs/regulatory/intern-salderen-2024.md
// §7). The remaining fields are curation provenance / audit (ADR-004) and the thin-event Summary.
type Ruling struct {
	// ECLI is the canonical ruling identifier. It is the IDENTITY key throughout: the ChangeEvent.Ref
	// the RaadVanStateSource emits MUST equal this, because assemble() looks the scope up by Ref.
	ECLI string

	// CompanionECLI is a parallel ruling in the same doctrinal shift, recorded for the audit trail but
	// NOT separately emitted (one canonical anchor — see note §6.1).
	CompanionECLI string

	// Court / RulingDate identify the deciding body and the date the doctrine changed. RulingDate is the
	// EffectiveAt of the emitted ChangeEvent and the scope.
	Court      string
	RulingDate time.Time

	// PredicateRoute is the doctrinal route string an assessment must rely on to be hit (matched against
	// NitrogenInputs.Routes in the evaluator's judge()), e.g. "intern_salderen".
	PredicateRoute string

	// Retroactive: when true the ruling reaches assessments authored BEFORE its effective date (the
	// evaluator's `applicable` is then always true). For 4923 this is true — the transition period is an
	// enforcement moratorium, not a cure (note §3); it is surfaced only in the recommendation text.
	Retroactive bool

	// Summary is the thin-event human "what changed" string. It is interpolated mid-sentence into the
	// evaluator's explanation after "...; ", so it must read cleanly there (note §5).
	Summary string

	// Recommendation is the sober, ADR-004-compliant remediation text embedded in the Finding
	// (CaseLawScope.Recommendation). Never "compliant"/"guaranteed" — see note §4.
	Recommendation string

	// Curation provenance / audit trail (ADR-004). Not consumed by judge().
	SourceURLs []string
	Confidence string
	CuratedOn  time.Time
	CuratedBy  string
}

// Registry holds the curated nitrogen rulings. It is the single, global source of case-law-abstraction
// truth (same for every tenant — ADR-009/010). Construct it with NewRegistry.
type Registry struct {
	rulings []Ruling          // in ascending ruling-date order (oldest first)
	byECLI  map[string]Ruling // keyed by ECLI for O(1) lookup
}

// ByECLI returns the curated ruling for an ECLI (the identity the scope provider / source key on).
func (r *Registry) ByECLI(ecli string) (Ruling, bool) {
	rul, ok := r.byECLI[ecli]
	return rul, ok
}

// All returns every curated ruling in ascending ruling-date order. The slice is a copy; mutating it
// does not affect the registry.
func (r *Registry) All() []Ruling {
	out := make([]Ruling, len(r.rulings))
	copy(out, r.rulings)
	return out
}

// Since returns the curated rulings whose RulingDate is strictly after t, in ascending ruling-date
// order. This is the watermark-driven lookup the RaadVanStateSource.Poll uses (mirrors the version
// watcher's "effective after `since`" semantics). The slice is freshly allocated.
func (r *Registry) Since(t time.Time) []Ruling {
	var out []Ruling
	for _, rul := range r.rulings { // ascending ruling-date order
		if rul.RulingDate.After(t) {
			out = append(out, rul)
		}
	}
	return out
}
