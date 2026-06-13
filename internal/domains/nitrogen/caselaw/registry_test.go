package caselaw

import (
	"testing"
	"time"
)

// ruling2024 is the single curated nitrogen ruling — ABRvS 18-12-2024, intern salderen weer
// vergunningplichtig. Its effective (ruling) date is the watermark boundary used throughout.
const ecli2024 = "ECLI:NL:RVS:2024:4923"

var rulingDate2024 = time.Date(2024, time.December, 18, 0, 0, 0, 0, time.UTC)

// M3 step 1 — ByECLI hit/miss. A hit returns the curated record; a miss must report not-found (the
// scope provider depends on this to refuse silently-defensible behaviour on an unknown ECLI).
func TestRegistry_ByECLI(t *testing.T) {
	reg := NewRegistry()

	cases := []struct {
		name    string
		ecli    string
		wantHit bool
	}{
		{"curated intern-salderen ruling is found", ecli2024, true},
		{"unknown ECLI is a miss", "ECLI:NL:RVS:2099:0001", false},
		{"empty ECLI is a miss", "", false},
		{"companion ruling is NOT separately indexed", "ECLI:NL:RVS:2024:4909", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := reg.ByECLI(tc.ecli)
			if ok != tc.wantHit {
				t.Fatalf("ByECLI(%q) ok = %v, want %v", tc.ecli, ok, tc.wantHit)
			}
			if tc.wantHit && got.ECLI != tc.ecli {
				t.Fatalf("ByECLI(%q).ECLI = %q, want %q", tc.ecli, got.ECLI, tc.ecli)
			}
			if !tc.wantHit && got.ECLI != "" {
				t.Fatalf("ByECLI(%q) miss returned a non-zero ruling %+v; want zero value", tc.ecli, got)
			}
		})
	}
}

// M3 step 1 — Since(t) is the watermark-driven lookup (strictly-after, mirrors the version watcher).
// Before the ruling date the ruling is emitted; on/after it is excluded.
func TestRegistry_Since(t *testing.T) {
	reg := NewRegistry()

	cases := []struct {
		name      string
		since     time.Time
		wantCount int
	}{
		{"cold watermark emits the ruling", time.Time{}, 1},
		{"day before the ruling emits it", rulingDate2024.AddDate(0, 0, -1), 1},
		{"exactly the ruling date excludes it (strictly-after)", rulingDate2024, 0},
		{"day after the ruling excludes it", rulingDate2024.AddDate(0, 0, 1), 0},
		{"far future excludes it", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reg.Since(tc.since)
			if len(got) != tc.wantCount {
				t.Fatalf("Since(%v) returned %d rulings, want %d", tc.since, len(got), tc.wantCount)
			}
			if tc.wantCount == 1 && got[0].ECLI != ecli2024 {
				t.Fatalf("Since(%v)[0].ECLI = %q, want %q", tc.since, got[0].ECLI, ecli2024)
			}
		})
	}
}

// All returns a copy in ascending ruling-date order; mutating it must not affect the registry.
func TestRegistry_All_IsDefensiveCopy(t *testing.T) {
	reg := NewRegistry()
	all := reg.All()
	if len(all) != 1 {
		t.Fatalf("All() returned %d rulings, want 1", len(all))
	}
	if all[0].ECLI != ecli2024 {
		t.Fatalf("All()[0].ECLI = %q, want %q", all[0].ECLI, ecli2024)
	}
	all[0].ECLI = "MUTATED" // mutate the returned slice
	again, ok := reg.ByECLI(ecli2024)
	if !ok || again.ECLI != ecli2024 {
		t.Fatalf("mutating All()'s result changed the registry; ByECLI returned %+v (ok=%v)", again, ok)
	}
}

// M3 step 1 — the curated facts that matter for the keep-alive behaviour (metadata/scope only; no
// physics). These are the values judge() and the recommendation depend on.
func TestRegistry_CuratedFacts(t *testing.T) {
	reg := NewRegistry()
	r, ok := reg.ByECLI(ecli2024)
	if !ok {
		t.Fatalf("curated ruling %q not found", ecli2024)
	}

	if r.PredicateRoute != "intern_salderen" {
		t.Errorf("PredicateRoute = %q, want intern_salderen", r.PredicateRoute)
	}
	if !r.Retroactive {
		t.Error("Retroactive = false, want true (the ruling reaches dossiers authored before its date)")
	}
	if !r.RulingDate.Equal(rulingDate2024) {
		t.Errorf("RulingDate = %v, want %v", r.RulingDate, rulingDate2024)
	}
	if r.Summary == "" {
		t.Error("Summary is empty; the thin-event narrative must be curated")
	}
	if r.Recommendation == "" {
		t.Error("Recommendation is empty; the Finding must carry sober remediation text (ADR-004)")
	}
}
