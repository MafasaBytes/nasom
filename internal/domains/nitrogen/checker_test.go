package nitrogen_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen"
)

// curated mitigation options (mirror of the production list) — used to assert the verdict→subset
// mapping precisely. If the production list changes, these must change too (intentional coupling: a
// silent change to the indicative options is a product/liability decision, ADR-004).
var (
	withMitigationSubset = []string{"elektrisch bouwmaterieel", "fasering van de bouw"}
	permitRequiredFull   = []string{
		"elektrisch bouwmaterieel",
		"fasering van de bouw",
		"intern of extern salderen",
		"emissiearme stalsystemen",
	}
)

// TestIndicativeChecker_Check proves the deterministic, gate-free Surface B pre-check (ADR-001):
//   - it yields each verdict for representative inputs,
//   - Indicative is ALWAYS true (it is never authoritative),
//   - EngineRef is ALWAYS "" (no official AERIUS Connect computation — ADR-001),
//   - the mitigations are exactly the curated subset that fits the verdict (none for buildable),
//   - and it never calls Connect (there is no engine to call; the checker takes none).
func TestIndicativeChecker_Check(t *testing.T) {
	checker := nitrogen.NewIndicativeChecker()

	cases := []struct {
		name            string
		inputs          nitrogen.NitrogenInputs
		wantVerdict     core.CheckVerdict
		wantMitigations []string
	}{
		{
			// Far from the area, tiny programme, low intensity → estimate stays in the buildable band.
			name: "low_impact_far_site_is_buildable",
			inputs: nitrogen.NitrogenInputs{
				Natura2000Area: "Veluwe",
				DistanceKm:     25,
				Homes:          5,
				BuildIntensity: 0.5,
			},
			wantVerdict:     core.VerdictBuildable,
			wantMitigations: nil,
		},
		{
			// Mid-distance, moderate programme → crosses the buildable band but not the permit band.
			name: "moderate_site_is_buildable_with_mitigation",
			inputs: nitrogen.NitrogenInputs{
				Natura2000Area: "Rijntakken",
				DistanceKm:     2,
				Homes:          200,
				BuildIntensity: 1,
			},
			wantVerdict:     core.VerdictBuildableWithMitigation,
			wantMitigations: withMitigationSubset,
		},
		{
			// Close, large, intense programme relying on offsetting routes → over the permit band.
			name: "close_intense_large_site_requires_permit",
			inputs: nitrogen.NitrogenInputs{
				Natura2000Area: "Veluwe",
				DistanceKm:     0.2,
				Homes:          800,
				CommercialM2:   50000,
				BuildIntensity: 2,
				Routes:         []string{"intern_salderen", "extern_salderen"},
			},
			wantVerdict:     core.VerdictPermitRequired,
			wantMitigations: permitRequiredFull,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.inputs)
			if err != nil {
				t.Fatalf("%s: marshal inputs: %v", tc.name, err)
			}

			out, err := checker.Check(context.Background(), raw)
			if err != nil {
				t.Fatalf("%s: Check returned error: %v", tc.name, err)
			}

			// ADR-001: a pre-check is NEVER authoritative.
			if !out.Indicative {
				t.Errorf("%s: Indicative = false, want true (a pre-check is never authoritative, ADR-001)", tc.name)
			}
			// ADR-001: it must NOT look like an official engine output.
			if out.Result.EngineRef != "" {
				t.Errorf("%s: Result.EngineRef = %q, want \"\" (no AERIUS Connect computation, ADR-001)", tc.name, out.Result.EngineRef)
			}
			// The verdict matches the band the estimate fell into.
			if out.Verdict != tc.wantVerdict {
				t.Errorf("%s: Verdict = %q, want %q", tc.name, out.Verdict, tc.wantVerdict)
			}
			// Mitigations are exactly the curated subset for the verdict (ADR-004: options, not guarantees).
			if !reflect.DeepEqual(out.Mitigations, tc.wantMitigations) {
				t.Errorf("%s: Mitigations = %v, want %v", tc.name, out.Mitigations, tc.wantMitigations)
			}
			// The indicative estimate must be surfaced as a deposition metric (sanity on the Result body).
			if _, ok := out.Result.Metrics["deposition_mol_ha_yr"]; !ok {
				t.Errorf("%s: Result.Metrics missing the deposition metric: %v", tc.name, out.Result.Metrics)
			}
		})
	}
}

// TestIndicativeChecker_Check_Deterministic proves the heuristic is deterministic: the same inputs
// always produce the same outcome (no clock, no randomness). This matters because the verdict is the
// status the promoted assessment inherits — a flaky verdict would be a flaky portfolio.
func TestIndicativeChecker_Check_Deterministic(t *testing.T) {
	checker := nitrogen.NewIndicativeChecker()
	raw, _ := json.Marshal(nitrogen.NitrogenInputs{Natura2000Area: "Veluwe", DistanceKm: 2, Homes: 200, BuildIntensity: 1})

	first, err := checker.Check(context.Background(), raw)
	if err != nil {
		t.Fatalf("first Check: %v", err)
	}
	for i := 0; i < 5; i++ {
		got, err := checker.Check(context.Background(), raw)
		if err != nil {
			t.Fatalf("repeat Check: %v", err)
		}
		if got.Verdict != first.Verdict || !reflect.DeepEqual(got.Mitigations, first.Mitigations) {
			t.Fatalf("non-deterministic: run %d = {%q %v}, first = {%q %v}", i, got.Verdict, got.Mitigations, first.Verdict, first.Mitigations)
		}
		if got.Result.Metrics["deposition_mol_ha_yr"] != first.Result.Metrics["deposition_mol_ha_yr"] {
			t.Fatalf("non-deterministic estimate: run %d = %v, first = %v", i, got.Result.Metrics, first.Result.Metrics)
		}
	}
}

// TestIndicativeChecker_Check_EmptyInputs proves empty inputs are handled gracefully (no parse
// error) and remain indicative / non-authoritative. With the zero-value programme the heuristic
// defaults intensity to 1 and distance to 0.1 km, giving est = base(0.05)/0.1 = 0.5 — exactly on the
// boundary, in the with-mitigation band (not over the permit band). The point of this test is the
// boundary handling and the ADR-001 flags, NOT a particular verdict; we assert the conservative
// posture (it must not be a silent "buildable" off zero inputs).
func TestIndicativeChecker_Check_EmptyInputs(t *testing.T) {
	checker := nitrogen.NewIndicativeChecker()
	out, err := checker.Check(context.Background(), nil)
	if err != nil {
		t.Fatalf("Check(nil): %v", err)
	}
	if !out.Indicative || out.Result.EngineRef != "" {
		t.Fatalf("empty inputs: Indicative=%v EngineRef=%q, want true/\"\"", out.Indicative, out.Result.EngineRef)
	}
	if out.Verdict != core.VerdictBuildableWithMitigation {
		t.Fatalf("empty inputs: Verdict = %q, want buildable_with_mitigation (est=0.5 on the boundary)", out.Verdict)
	}
	if !reflect.DeepEqual(out.Mitigations, withMitigationSubset) {
		t.Fatalf("empty inputs: Mitigations = %v, want %v", out.Mitigations, withMitigationSubset)
	}
}

// TestIndicativeChecker_Check_MalformedInputs proves malformed JSON surfaces a parse error rather than
// silently producing a (possibly false-buildable) verdict — the conservative posture (ADR-004).
func TestIndicativeChecker_Check_MalformedInputs(t *testing.T) {
	checker := nitrogen.NewIndicativeChecker()
	if _, err := checker.Check(context.Background(), json.RawMessage(`{not json`)); err == nil {
		t.Fatal("Check on malformed JSON returned nil error; want a parse error (never a silent verdict, ADR-004)")
	}
}
