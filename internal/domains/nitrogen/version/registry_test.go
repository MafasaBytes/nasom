package version

import (
	"testing"
	"time"
)

// M2 step 1 — the version registry and the ONE curated migration (ADR-003).
//
// These assertions are about METADATA / NARRATIVE / SCHEMA-VERSION and the re-evaluation trigger —
// NOT physics. The version layer never computes a deposition; it triggers and explains a recompute
// (ADR-001/009). So we assert the curated facts that drive the keep-alive motion (identity, effective
// date, supersedes chain, the "identical inputs may differ" trigger, the encoded IMAER-exact
// uncertainty), and deliberately assert NO emission-factor values.

func TestRegistry_Latest_Is2025(t *testing.T) {
	reg := NewRegistry()

	got, ok := reg.Latest()
	if !ok {
		t.Fatal("Latest() ok = false, want true (curated registry holds releases)")
	}
	if got.VersionKey != "2025" {
		t.Fatalf("Latest().VersionKey = %q, want %q", got.VersionKey, "2025")
	}
	if got.Label != "AERIUS Calculator 2025" {
		t.Fatalf("Latest().Label = %q, want %q", got.Label, "AERIUS Calculator 2025")
	}
	wantEffective := time.Date(2025, time.October, 7, 0, 0, 0, 0, time.UTC)
	if !got.EffectiveDate.Equal(wantEffective) {
		t.Fatalf("Latest().EffectiveDate = %v, want %v (mandatory from 7 Oct 2025)", got.EffectiveDate, wantEffective)
	}
	if got.Supersedes != "2024" {
		t.Fatalf("Latest().Supersedes = %q, want %q", got.Supersedes, "2024")
	}
}

func TestRegistry_Releases_AscendingOrder(t *testing.T) {
	reg := NewRegistry()

	rels := reg.Releases()
	if len(rels) != 2 {
		t.Fatalf("Releases() len = %d, want 2 (2024, 2025)", len(rels))
	}
	if rels[0].VersionKey != "2024" || rels[1].VersionKey != "2025" {
		t.Fatalf("Releases() order = [%q, %q], want [2024, 2025] (oldest first)", rels[0].VersionKey, rels[1].VersionKey)
	}
	if !rels[0].EffectiveDate.Before(rels[1].EffectiveDate) {
		t.Fatalf("Releases() not in ascending effective-date order: %v then %v", rels[0].EffectiveDate, rels[1].EffectiveDate)
	}
}

func TestRegistry_Get_HitAndMiss(t *testing.T) {
	reg := NewRegistry()

	cases := []struct {
		name       string
		key        string
		wantOK     bool
		wantLabel  string
		wantSchema string
	}{
		{"2024 hit", "2024", true, "AERIUS Calculator 2024", "6.0.x"},
		{"2025 hit", "2025", true, "AERIUS Calculator 2025", "6.0.x"},
		{"unknown miss", "2099", false, "", ""},
		{"empty key miss", "", false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel, ok := reg.Get(tc.key)
			if ok != tc.wantOK {
				t.Fatalf("Get(%q) ok = %v, want %v", tc.key, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if rel.Label != tc.wantLabel {
				t.Fatalf("Get(%q).Label = %q, want %q", tc.key, rel.Label, tc.wantLabel)
			}
			if rel.ImaerLine != tc.wantSchema {
				t.Fatalf("Get(%q).ImaerLine = %q, want %q", tc.key, rel.ImaerLine, tc.wantSchema)
			}
		})
	}
}

// The one tested migration. Asserts the curated metadata/narrative facts that make keep-alive fire,
// NOT factor physics.
func TestRegistry_Delta_2024to2025(t *testing.T) {
	reg := NewRegistry()

	d, ok := reg.Delta("2024", "2025")
	if !ok {
		t.Fatal("Delta(2024,2025) ok = false, want true (the curated migration must exist)")
	}
	if d.FromVersion != "2024" || d.ToVersion != "2025" {
		t.Fatalf("Delta from->to = %q->%q, want 2024->2025", d.FromVersion, d.ToVersion)
	}

	// The keep-alive trigger: identical inputs may differ across the version, so a recompute is required
	// and the authoritative answer is the Connect recompute — direction is never a substitute (ADR-009).
	if !d.IdenticalInputsMayDiffer {
		t.Fatal("Delta.IdenticalInputsMayDiffer = false, want true (this is WHY keep-alive exists)")
	}
	if d.ReevalTrigger != "recompute_required" {
		t.Fatalf("Delta.ReevalTrigger = %q, want %q", d.ReevalTrigger, "recompute_required")
	}
	if d.AuthoritativeSource != "rivm_connect_recompute" {
		t.Fatalf("Delta.AuthoritativeSource = %q, want %q (recompute is authoritative, not direction)", d.AuthoritativeSource, "rivm_connect_recompute")
	}
	if d.Summary == "" {
		t.Fatal("Delta.Summary is empty; the human 'what changed' narrative must be present")
	}

	// The IMAER-exact-version uncertainty for the Oct-2025 build is encoded honestly (§6 convention):
	// the exact patch is unconfirmed against live Connect, so it must NOT be presented as confirmed.
	rel2025, _ := reg.Get("2025")
	if rel2025.ImaerExactConfirmed {
		t.Fatal("2025.ImaerExactConfirmed = true, want false (exact IMAER build is unconfirmed vs. live Connect)")
	}
	if rel2025.ImaerExactVersion != "" {
		t.Fatalf("2025.ImaerExactVersion = %q, want empty (unconfirmed exact build must not be hard-coded)", rel2025.ImaerExactVersion)
	}
	foundUncertainty := false
	for _, fl := range d.UncertaintyFlags {
		if fl == "imaer_exact_version_unconfirmed" {
			foundUncertainty = true
			break
		}
	}
	if !foundUncertainty {
		t.Fatalf("Delta.UncertaintyFlags = %v, want it to carry imaer_exact_version_unconfirmed", d.UncertaintyFlags)
	}
}

// No reverse delta is curated; Delta misses must report not-found rather than fabricate one.
func TestRegistry_Delta_Miss(t *testing.T) {
	reg := NewRegistry()

	if _, ok := reg.Delta("2025", "2024"); ok {
		t.Fatal("Delta(2025,2024) ok = true, want false (no reverse delta is curated)")
	}
	if _, ok := reg.Delta("2023", "2024"); ok {
		t.Fatal("Delta(2023,2024) ok = true, want false (not curated)")
	}
}

// Releases() returns a copy: mutating the returned slice must not corrupt the registry.
func TestRegistry_Releases_ReturnsCopy(t *testing.T) {
	reg := NewRegistry()

	rels := reg.Releases()
	rels[0] = AeriusRelease{Label: "TAMPERED"}

	again := reg.Releases()
	if again[0].Label == "TAMPERED" {
		t.Fatal("Releases() shares backing state; mutating the result corrupted the registry")
	}
}
