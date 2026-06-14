package nitrogen

import (
	"context"
	"time"

	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen/version"
)

// This file implements the M2 version-abstraction wiring for the nitrogen vertical:
//   - AeriusReleaseWatcher (core.RuleVersionSource) — registry-backed release watcher.
//   - RegistryVersionDeltaProvider (nitrogen.VersionDeltaProvider) — the REAL-PATH provider that
//     maps a curated version.AeriusReleaseDelta to the evaluator's VersionDelta.
//
// Both read the global version.Registry (ADR-003). The registry holds only metadata / narrative /
// schema-version / coarse direction — NO physics (ADR-001/009). Direction-of-impact carried in the
// registry is TRIAGE ONLY; nothing here lets it set or skip a DefensibilityStatus — status comes
// solely from the Connect recompute vs. the KDW in the evaluator's judge().

// ---- AeriusReleaseWatcher : core.RuleVersionSource -------------------------

// AeriusReleaseWatcher watches the (annual) AERIUS release via the curated version.Registry and
// emits THIN ChangeEvent{Kind: ChangeRuleVersion} for each release effective after a watermark.
// assemble() (ADR-009) later looks up the curated delta by identity and recomputes via Connect; the
// event itself stays thin (identity + curated narrative summary), per ADR-009's thin-event design.
//
// `now` is injected so IngestedAt is deterministic in tests; it defaults to time.Now.
type AeriusReleaseWatcher struct {
	registry *version.Registry
	now      func() time.Time
}

// NewAeriusReleaseWatcher constructs the watcher over a registry. If registry is nil it falls back
// to the curated registry (version.NewRegistry); if now is nil it defaults to time.Now.
func NewAeriusReleaseWatcher(registry *version.Registry, now func() time.Time) *AeriusReleaseWatcher {
	if registry == nil {
		registry = version.NewRegistry()
	}
	if now == nil {
		now = time.Now
	}
	return &AeriusReleaseWatcher{registry: registry, now: now}
}

// Current returns the registry's latest curated release as a core.RuleVersionRef.
func (w *AeriusReleaseWatcher) Current(ctx context.Context) (core.RuleVersionRef, error) {
	rel, ok := w.registry.Latest()
	if !ok {
		return core.RuleVersionRef{}, errNoCuratedReleases
	}
	return releaseToRef(rel), nil
}

// Poll returns a thin ChangeEvent{Kind: ChangeRuleVersion} for each curated release whose mandatory
// effective date is strictly after `since`. Events carry:
//   - Ref         = the release label (the identity assemble() looks up curated detail by),
//   - Summary     = the curated delta narrative for from->to (human "what changed"),
//   - EffectiveAt = the mandatory effective date,
//   - IngestedAt  = injected now() (deterministic in tests),
//   - Domain      = nitrogen.
//
// Releases are emitted in ascending effective-date order. A release with no curated inbound delta
// (e.g. the oldest known release, or a gap) still emits, with an empty Summary — assemble() resolves
// the delta narrative via the RegistryVersionDeltaProvider against the assessment's own from-version.
func (w *AeriusReleaseWatcher) Poll(ctx context.Context, since time.Time) ([]core.ChangeEvent, error) {
	ingestedAt := w.now()
	var out []core.ChangeEvent
	for _, rel := range w.registry.Releases() { // ascending release order
		if !rel.EffectiveDate.After(since) {
			continue
		}
		summary := ""
		if d, ok := w.registry.Delta(rel.Supersedes, rel.VersionKey); ok {
			summary = d.Summary
		}
		out = append(out, core.ChangeEvent{
			// Stable, deterministic id per release so events don't collide on a "" key in the
			// repository and Findings can reliably reference their change event.
			ID:          core.ChangeEventID("aerius-release-" + rel.VersionKey),
			Domain:      core.DomainNitrogen,
			Kind:        core.ChangeRuleVersion,
			Ref:         rel.Label,
			Summary:     summary,
			EffectiveAt: rel.EffectiveDate,
			IngestedAt:  ingestedAt,
		})
	}
	return out, nil
}

// ---- RegistryVersionDeltaProvider : nitrogen.VersionDeltaProvider ----------

// RegistryVersionDeltaProvider is the REAL-PATH VersionDeltaProvider: it maps a curated
// version.AeriusReleaseDelta from the registry to the evaluator's VersionDelta, using the curated
// narrative as the human Summary. (StaticVersionDeltaProvider remains for unit tests.)
//
// It maps by version_key, which the registry stores in orderable form. The from/to refs carry
// AERIUS labels (e.g. "AERIUS Calculator 2025"); versionKey() extracts the comparable key.
type RegistryVersionDeltaProvider struct {
	registry *version.Registry
}

// NewRegistryVersionDeltaProvider constructs the provider over a registry. If registry is nil it
// falls back to the curated registry (version.NewRegistry).
func NewRegistryVersionDeltaProvider(registry *version.Registry) RegistryVersionDeltaProvider {
	if registry == nil {
		registry = version.NewRegistry()
	}
	return RegistryVersionDeltaProvider{registry: registry}
}

// Between maps the curated registry delta (from -> to) to the evaluator's VersionDelta. The
// VersionDelta intentionally carries ONLY labels + the curated human Summary — no direction,
// categories, or physics cross this boundary into judge() (ADR-009: judge sets status from the
// recompute vs. KDW alone). If no curated delta exists for the pair, it returns a minimal
// label-only VersionDelta so the keep-alive motion still proceeds (the recompute is authoritative).
func (p RegistryVersionDeltaProvider) Between(ctx context.Context, from, to core.RuleVersionRef) (VersionDelta, error) {
	fromKey := versionKeyFromRef(from, p.registry)
	toKey := versionKeyFromRef(to, p.registry)
	if d, ok := p.registry.Delta(fromKey, toKey); ok {
		return VersionDelta{FromLabel: from.Label, ToLabel: to.Label, Summary: d.Summary}, nil
	}
	return VersionDelta{FromLabel: from.Label, ToLabel: to.Label}, nil
}

// ---- helpers ---------------------------------------------------------------

// releaseToRef converts a curated release to a core.RuleVersionRef. The Label is the AERIUS release
// label; the EffectiveAt is the mandatory date.
func releaseToRef(rel version.AeriusRelease) core.RuleVersionRef {
	return core.RuleVersionRef{
		Domain:      core.DomainNitrogen,
		Label:       rel.Label,
		EffectiveAt: rel.EffectiveDate,
	}
}

// versionKeyFromRef resolves a RuleVersionRef to a registry version_key. It first tries an exact
// label match against the registry's releases (the robust path); failing that it returns the raw
// label so a caller using bare keys ("2024") still resolves.
func versionKeyFromRef(ref core.RuleVersionRef, registry *version.Registry) string {
	for _, rel := range registry.Releases() {
		if rel.Label == ref.Label {
			return rel.VersionKey
		}
	}
	return ref.Label
}

// errNoCuratedReleases is returned by Current when the registry holds no releases (should not occur
// with the curated registry; guards against an empty injected registry).
var errNoCuratedReleases = &noCuratedReleasesError{}

type noCuratedReleasesError struct{}

func (*noCuratedReleasesError) Error() string {
	return "nitrogen: version registry holds no curated AERIUS releases"
}

// Compile-time assertions that the M2 adapters satisfy their ports.
var (
	_ core.RuleVersionSource = (*AeriusReleaseWatcher)(nil)
	_ VersionDeltaProvider   = RegistryVersionDeltaProvider{}
)
