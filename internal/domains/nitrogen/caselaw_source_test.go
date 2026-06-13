package nitrogen

import (
	"context"
	"testing"
	"time"

	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen/caselaw"
)

// Curated identity/date for the one nitrogen ruling — the values the source emits and the scope
// provider keys on. The ECLI here MUST be the Ref the source emits AND the scope-provider key.
const caselawECLI = "ECLI:NL:RVS:2024:4923"

var (
	caselawDate = time.Date(2024, time.December, 18, 0, 0, 0, 0, time.UTC)
	// ingestClock is the injected wall clock — makes IngestedAt deterministic.
	ingestClock = time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)
)

// M3 step 2 — RaadVanStateSource.Poll emits a thin ChangeEvent for the ruling when the watermark
// predates it, and nothing once the watermark is on/after it. IngestedAt comes from the injected
// clock (deterministic). fixedNow is defined in version_source_test.go (same package).
func TestRaadVanStateSource_Poll(t *testing.T) {
	cases := []struct {
		name      string
		since     time.Time
		wantEvent bool
	}{
		{"cold watermark emits the ruling", time.Time{}, true},
		{"day before the ruling emits it", caselawDate.AddDate(0, 0, -1), true},
		{"exactly the ruling date emits nothing (strictly-after)", caselawDate, false},
		{"after the ruling emits nothing", caselawDate.AddDate(0, 0, 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := NewRaadVanStateSource(caselaw.NewRegistry(), fixedNow(ingestClock))
			events, err := src.Poll(context.Background(), tc.since)
			if err != nil {
				t.Fatalf("Poll: %v", err)
			}
			if !tc.wantEvent {
				if len(events) != 0 {
					t.Fatalf("Poll(%v) emitted %d events, want 0", tc.since, len(events))
				}
				return
			}
			if len(events) != 1 {
				t.Fatalf("Poll(%v) emitted %d events, want 1", tc.since, len(events))
			}
			e := events[0]
			if e.Domain != core.DomainNitrogen {
				t.Errorf("event.Domain = %q, want nitrogen", e.Domain)
			}
			if e.Kind != core.ChangeCaseLaw {
				t.Errorf("event.Kind = %q, want case_law", e.Kind)
			}
			if e.Ref != caselawECLI {
				t.Errorf("event.Ref = %q, want %q (the ECLI the scope provider keys on)", e.Ref, caselawECLI)
			}
			if !e.EffectiveAt.Equal(caselawDate) {
				t.Errorf("event.EffectiveAt = %v, want %v (the ruling date)", e.EffectiveAt, caselawDate)
			}
			if !e.IngestedAt.Equal(ingestClock) {
				t.Errorf("event.IngestedAt = %v, want injected clock %v (deterministic)", e.IngestedAt, ingestClock)
			}
			if e.Summary == "" {
				t.Error("event.Summary is empty; the thin event must carry the curated narrative")
			}
		})
	}
}

// The Ref emitted by the source MUST equal the key the scope provider resolves — they are wired
// to the SAME registry, so a curated ruling is always resolvable end-to-end. This guards the
// thin-event contract (assemble() looks up scope by Ref).
func TestRaadVanStateSource_RefMatchesScopeProviderKey(t *testing.T) {
	reg := caselaw.NewRegistry()
	src := NewRaadVanStateSource(reg, fixedNow(ingestClock))
	provider := NewRegistryCaseLawScopeProvider(reg)

	events, err := src.Poll(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("Poll emitted %d events, want 1", len(events))
	}
	// The provider must resolve the very event the source emitted, by Ref.
	scope, err := provider.Scope(context.Background(), events[0])
	if err != nil {
		t.Fatalf("Scope for the source's own emitted Ref %q failed: %v", events[0].Ref, err)
	}
	if scope.ECLI != events[0].Ref {
		t.Fatalf("resolved scope.ECLI = %q, want event.Ref %q", scope.ECLI, events[0].Ref)
	}
}

// M3 step 3 — RegistryCaseLawScopeProvider.Scope returns the curated scope for the known ECLI, and an
// unknown ECLI returns an ERROR (never a silent/empty scope). A silent-defensible default here would
// be a liability bug (ADR-004) — so we assert the error explicitly.
func TestRegistryCaseLawScopeProvider_Scope(t *testing.T) {
	provider := NewRegistryCaseLawScopeProvider(caselaw.NewRegistry())

	t.Run("known ECLI returns the curated scope", func(t *testing.T) {
		e := core.ChangeEvent{Domain: core.DomainNitrogen, Kind: core.ChangeCaseLaw, Ref: caselawECLI}
		scope, err := provider.Scope(context.Background(), e)
		if err != nil {
			t.Fatalf("Scope(known): %v", err)
		}
		if scope.ECLI != caselawECLI {
			t.Errorf("scope.ECLI = %q, want %q", scope.ECLI, caselawECLI)
		}
		if scope.PredicateRoute != "intern_salderen" {
			t.Errorf("scope.PredicateRoute = %q, want intern_salderen", scope.PredicateRoute)
		}
		if !scope.Retroactive {
			t.Error("scope.Retroactive = false, want true")
		}
		if !scope.EffectiveAt.Equal(caselawDate) {
			t.Errorf("scope.EffectiveAt = %v, want %v", scope.EffectiveAt, caselawDate)
		}
		if scope.Recommendation == "" {
			t.Error("scope.Recommendation is empty; the Finding needs sober remediation text (ADR-004)")
		}
	})

	t.Run("unknown ECLI returns an error, never a silent scope", func(t *testing.T) {
		e := core.ChangeEvent{Domain: core.DomainNitrogen, Kind: core.ChangeCaseLaw, Ref: "ECLI:NL:RVS:2099:0001"}
		scope, err := provider.Scope(context.Background(), e)
		if err == nil {
			t.Fatalf("Scope(unknown) returned nil error with scope %+v; an unknown ruling MUST error (never silent-defensible — ADR-004)", scope)
		}
		if scope != (CaseLawScope{}) {
			t.Fatalf("Scope(unknown) returned a non-zero scope %+v alongside the error; want the zero value", scope)
		}
	})
}
