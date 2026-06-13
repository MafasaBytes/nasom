// Package version is the first-class version-abstraction layer for the nitrogen vertical (ADR-003).
//
// It isolates ALL version-specific knowledge about AERIUS annual releases — release identity,
// the curated "what-changed" narrative, the IMAER/GML schema version, and a COARSE expected
// direction of impact — so that a new release is a contained, testable migration. This layer is
// the keep-alive product, not plumbing.
//
// Hard boundaries (do not cross):
//   - NO PHYSICS. There are no emission-factor tables, dispersion parameters, or background-
//     deposition maps here. The legally authoritative deposition number is ALWAYS RIVM-computed
//     via AERIUS Connect (ADR-001/009). This package triggers and EXPLAINS a re-evaluation; it
//     never calculates one.
//   - DIRECTION OF IMPACT IS TRIAGE/PRIORITISATION ONLY (ADR-009 + the analyst's convention).
//     The curated direction fields exist to rank/order which recomputes to surface first and to
//     enrich the human explanation. They MUST NEVER set or skip a DefensibilityStatus. Status
//     comes solely from the Connect recompute vs. the KDW (that logic lives in the evaluator's
//     judge()).
//
// All curated values in this package are sourced from the regulatory-caselaw-analyst's cited note
// docs/regulatory/aerius-2025-release.md (curated 2026-06-13). Citations are inline.
package version

import "time"

// ChangeCategory is a coarse category of change between two releases (NOT factor values).
// Source: docs/regulatory/aerius-2025-release.md §3, §8b.
type ChangeCategory string

const (
	CategoryEmissionFactors        ChangeCategory = "emission_factors"
	CategorySourceCharacterisation ChangeCategory = "source_characterisation"
	CategoryDispersionModel        ChangeCategory = "dispersion_model"
	CategoryBackgroundMaps         ChangeCategory = "background_maps"
	CategoryNatureHabitatData      ChangeCategory = "nature_habitat_data"
	CategoryIMAERModel             ChangeCategory = "imaer_model"
)

// ExpectedDirection is a COARSE, release-wide direction-of-impact signal. TRIAGE ONLY — never an
// input to a DefensibilityStatus decision (ADR-009). Source: §8b expected_direction enum.
type ExpectedDirection string

const (
	DirectionMixedSourceDependent ExpectedDirection = "mixed_source_dependent"
	DirectionMostlyIncrease       ExpectedDirection = "mostly_increase"
	DirectionMostlyDecrease       ExpectedDirection = "mostly_decrease"
	DirectionNeutral              ExpectedDirection = "neutral"
	DirectionUnknown              ExpectedDirection = "unknown"
)

// SourceDirection is a per-source-type directional signal. TRIAGE/PRIORITISATION ONLY (ADR-009):
// used to rank which recomputes to surface first; never to set or skip a status, and never to
// down-rank an assessment to "less likely exposed" (always recompute). Source: §4, §8b.
type SourceDirection struct {
	SourceType string // e.g. "shipping_nox", "mobile_equipment_small"
	Direction  string // "increase" | "decrease" | "neutral" | "minor"
	Confidence string // "indicative" — secondary commentary, not authoritative
}

// PointRelease is an intra-year point release (e.g. 2025.2). ResultsAffected is verified per
// release (UX/technical-only releases do not warrant a portfolio-wide change-event). Source: §1, §8a.
type PointRelease struct {
	Key             string
	Date            time.Time
	ResultsAffected bool
}

// AeriusRelease is the per-release metadata record (ADR-003). It is strictly metadata / narrative /
// schema-version — NO factor tables. Shape per docs/regulatory/aerius-2025-release.md §8a.
type AeriusRelease struct {
	Label      string   // "AERIUS Calculator 2025"
	VersionKey string   // "2025" — orderable key for the evaluator's computed_under_version < to predicate
	Products   []string // ["Calculator","Connect","Monitor"] — updated together

	EffectiveDate time.Time // mandatory under the Omgevingsregeling
	Supersedes    string    // prior annual release version_key

	// IMAER / GML schema version. ImaerExactVersion is empty and ImaerExactConfirmed is false when
	// the exact patch bound to this build is unconfirmed against live Connect/handbook (§6, §8a).
	ImaerLine            string // "6.0.x" (GML-SF2 / GML 3.2.1)
	ImaerExactVersion    string // "" when unconfirmed
	ImaerExactConfirmed  bool   // false until the integration specialist confirms vs. live Connect
	GMLProfile           string // "GML 3.2.1 SF2 (GML-SF2)"
	BreakingSchemaChange bool   // no evidence of a major IMAER bump at 2025

	PointReleases []PointRelease

	SourceURLs []string  // primary URLs backing this record (changelog/audit trail, ADR-004)
	CuratedOn  time.Time // provenance
	CuratedBy  string
}

// AeriusReleaseDelta is the per-release-to-release delta record (ADR-003). Coarse categories +
// narrative + triage direction + the re-evaluation trigger. NO factor values. Shape per §8b.
type AeriusReleaseDelta struct {
	FromVersion string // "2024"
	ToVersion   string // "2025"

	ChangeCategories []ChangeCategory
	CategoryNotes    map[ChangeCategory]string // short narrative per category (NOT factor values)

	ExpectedDirection ExpectedDirection // coarse release-wide signal — TRIAGE ONLY
	DirectionBySource []SourceDirection // per-source signals — TRIAGE ONLY

	IdenticalInputsMayDiffer bool   // true (confirmed — BMD Advies); this is WHY keep-alive exists
	ReevalTrigger            string // "recompute_required"
	ReevalPredicate          string // "computed_under_version < \"2025\" AND route_uses_connect"
	AuthoritativeSource      string // "rivm_connect_recompute" — direction never substitutes for it

	// Summary is the curated human narrative used as the evaluator's VersionDelta.Summary.
	Summary string

	Confidence       string   // "high (identity/date/trigger); medium (imaer exact; direction granularity)"
	UncertaintyFlags []string // open verification items — keeps unconfirmed facts honest
	SourceURLs       []string
	CuratedOn        time.Time
	CuratedBy        string
}

// Registry holds the curated AERIUS releases and the release-to-release deltas. It is the single,
// global source of version-abstraction truth (same for every tenant — ADR-009/010). Construct it
// with NewRegistry to get the curated 2024 + 2025 releases and the 2024->2025 delta.
type Registry struct {
	releases map[string]AeriusRelease      // keyed by VersionKey
	order    []string                      // VersionKeys in ascending release order (oldest -> newest)
	deltas   map[string]AeriusReleaseDelta // keyed by "from->to"
}

func deltaKey(from, to string) string { return from + "->" + to }

// Latest returns the most recent curated release (highest in release order). The bool is false only
// if the registry holds no releases.
func (r *Registry) Latest() (AeriusRelease, bool) {
	if len(r.order) == 0 {
		return AeriusRelease{}, false
	}
	return r.releases[r.order[len(r.order)-1]], true
}

// Get returns the release for a version key (e.g. "2025").
func (r *Registry) Get(versionKey string) (AeriusRelease, bool) {
	rel, ok := r.releases[versionKey]
	return rel, ok
}

// Releases returns the curated releases in ascending release order (oldest first). The slice is a
// copy; mutating it does not affect the registry.
func (r *Registry) Releases() []AeriusRelease {
	out := make([]AeriusRelease, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, r.releases[k])
	}
	return out
}

// Delta returns the curated delta for a from->to transition (e.g. "2024" -> "2025").
func (r *Registry) Delta(from, to string) (AeriusReleaseDelta, bool) {
	d, ok := r.deltas[deltaKey(from, to)]
	return d, ok
}
