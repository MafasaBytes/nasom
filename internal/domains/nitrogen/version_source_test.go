package nitrogen

import (
	"context"
	"testing"
	"time"

	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen/version"
)

// M2 step 2 — the AeriusReleaseWatcher (core.RuleVersionSource) over the curated registry.
//
// The watcher is THIN-event by design (ADR-009): Current() reports the authoritative version;
// Poll(since) emits a ChangeEvent{Kind: ChangeRuleVersion, Domain: nitrogen} per release effective
// AFTER `since`, carrying Ref=label, Summary=curated narrative, EffectiveAt=mandatory date, and a
// deterministic IngestedAt from the injected clock. We pin the clock so IngestedAt is exact.

var watcherClock = time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestAeriusReleaseWatcher_Current_IsLatest(t *testing.T) {
	reg := version.NewRegistry()
	w := NewAeriusReleaseWatcher(reg, fixedNow(watcherClock))

	ref, err := w.Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if ref.Domain != core.DomainNitrogen {
		t.Fatalf("Current().Domain = %q, want %q", ref.Domain, core.DomainNitrogen)
	}
	if ref.Label != "AERIUS Calculator 2025" {
		t.Fatalf("Current().Label = %q, want the 2025 release label", ref.Label)
	}
	wantEffective := time.Date(2025, time.October, 7, 0, 0, 0, 0, time.UTC)
	if !ref.EffectiveAt.Equal(wantEffective) {
		t.Fatalf("Current().EffectiveAt = %v, want %v", ref.EffectiveAt, wantEffective)
	}
}

func TestAeriusReleaseWatcher_Poll(t *testing.T) {
	d2024 := time.Date(2024, time.October, 1, 0, 0, 0, 0, time.UTC)
	d2025 := time.Date(2025, time.October, 7, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		since      time.Time
		wantRefs   []string // expected ChangeEvent.Ref values, in emit order (ascending effective date)
		wantSecond string   // for the partial case, the single ref expected
	}{
		{
			name:     "cold watermark emits all releases ascending",
			since:    time.Time{}, // zero time — the worker's first run
			wantRefs: []string{"AERIUS Calculator 2024", "AERIUS Calculator 2025"},
		},
		{
			name:     "since between releases emits only 2025",
			since:    d2024, // exactly the 2024 effective date — strictly-after excludes 2024, includes 2025
			wantRefs: []string{"AERIUS Calculator 2025"},
		},
		{
			name:     "since after 2025 emits nothing",
			since:    d2025, // exactly the 2025 effective date — strictly-after excludes it
			wantRefs: nil,
		},
		{
			name:     "since far future emits nothing",
			since:    time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
			wantRefs: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := version.NewRegistry()
			w := NewAeriusReleaseWatcher(reg, fixedNow(watcherClock))

			events, err := w.Poll(context.Background(), tc.since)
			if err != nil {
				t.Fatalf("Poll: %v", err)
			}
			if len(events) != len(tc.wantRefs) {
				t.Fatalf("Poll(%v) emitted %d events %+v, want %d (%v)", tc.since, len(events), refs(events), len(tc.wantRefs), tc.wantRefs)
			}
			for i, e := range events {
				if e.Ref != tc.wantRefs[i] {
					t.Fatalf("event[%d].Ref = %q, want %q", i, e.Ref, tc.wantRefs[i])
				}
				// Thin-event shape invariants on every emitted event.
				if e.Domain != core.DomainNitrogen {
					t.Fatalf("event[%d].Domain = %q, want nitrogen", i, e.Domain)
				}
				if e.Kind != core.ChangeRuleVersion {
					t.Fatalf("event[%d].Kind = %q, want rule_version", i, e.Kind)
				}
				if e.EffectiveAt.IsZero() {
					t.Fatalf("event[%d].EffectiveAt is zero; must carry the mandatory effective date", i)
				}
				if !e.IngestedAt.Equal(watcherClock) {
					t.Fatalf("event[%d].IngestedAt = %v, want injected clock %v (deterministic)", i, e.IngestedAt, watcherClock)
				}
			}

			// The 2025 event must carry the curated delta narrative as its Summary (thin event ->
			// human "what changed"); the 2024 baseline event has no curated inbound delta so its
			// Summary is empty (assemble() resolves the narrative against the assessment's from-version).
			for _, e := range events {
				switch e.Ref {
				case "AERIUS Calculator 2025":
					if e.Summary == "" {
						t.Fatal("2025 event Summary is empty; want the curated 2024->2025 narrative")
					}
				case "AERIUS Calculator 2024":
					if e.Summary != "" {
						t.Fatalf("2024 baseline event Summary = %q, want empty (no curated inbound delta)", e.Summary)
					}
				}
			}

			_ = d2025 // referenced via cases
		})
	}
}

func refs(events []core.ChangeEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Ref
	}
	return out
}

// RegistryVersionDeltaProvider maps a from->to ref pair to the curated VersionDelta. With the curated
// pair it carries the narrative Summary; with an unknown pair it returns a minimal label-only delta so
// the keep-alive motion still proceeds (the recompute is authoritative — ADR-009).
func TestRegistryVersionDeltaProvider_Between(t *testing.T) {
	reg := version.NewRegistry()
	p := NewRegistryVersionDeltaProvider(reg)

	from := core.RuleVersionRef{Domain: core.DomainNitrogen, Label: "AERIUS Calculator 2024"}
	to := core.RuleVersionRef{Domain: core.DomainNitrogen, Label: "AERIUS Calculator 2025"}

	d, err := p.Between(context.Background(), from, to)
	if err != nil {
		t.Fatalf("Between: %v", err)
	}
	if d.FromLabel != from.Label || d.ToLabel != to.Label {
		t.Fatalf("Between labels = %q->%q, want %q->%q", d.FromLabel, d.ToLabel, from.Label, to.Label)
	}
	if d.Summary == "" {
		t.Fatal("Between Summary is empty for the curated pair; want the 2024->2025 narrative")
	}

	// Unknown pair: label-only delta, no error (motion proceeds; recompute is authoritative).
	unknown := core.RuleVersionRef{Domain: core.DomainNitrogen, Label: "AERIUS Calculator 2099"}
	d2, err := p.Between(context.Background(), to, unknown)
	if err != nil {
		t.Fatalf("Between(unknown): %v", err)
	}
	if d2.Summary != "" {
		t.Fatalf("Between(unknown) Summary = %q, want empty (no curated delta)", d2.Summary)
	}
	if d2.FromLabel != to.Label || d2.ToLabel != unknown.Label {
		t.Fatalf("Between(unknown) labels = %q->%q, want %q->%q", d2.FromLabel, d2.ToLabel, to.Label, unknown.Label)
	}
}
