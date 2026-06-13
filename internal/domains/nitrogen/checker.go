package nitrogen

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/houvast/houvast/internal/core"
)

// IndicativeChecker is the nitrogen Surface B pre-check (core.LocationChecker). It is a fast,
// deterministic HEURISTIC over NitrogenInputs — NOT the authoritative AERIUS Connect engine.
//
// AUTHORITATIVE BOUNDARY (ADR-001): the estimate this produces is INDICATIVE only. It never calls
// AERIUS Connect and is never a legally-valid number. A site promoted into the monitor as a
// decision-bearing assessment must still be backed by an official CalculationEngine computation,
// authored by the customer/consultant (ADR-004). The heuristic exists for speed on the on-ramp,
// to give a developer an instant "can I build here, how defensible?" read before committing to a
// full Connect computation.
//
// LIABILITY POSTURE (ADR-004): the verdict and mitigations are INDICATIVE OPTIONS, never
// guarantees ("gegarandeerd"/"compliant" copy is forbidden).
type IndicativeChecker struct{}

// NewIndicativeChecker builds the gate-free nitrogen pre-check. It takes no engine (it never calls
// Connect) and no per-tenant config (the heuristic is global).
func NewIndicativeChecker() *IndicativeChecker { return &IndicativeChecker{} }

// curated mitigation options (Dutch), most-impactful first. Subsets are selected per verdict.
// Source: research notes. These are INDICATIVE options, not guarantees (ADR-004).
var mitigationOptions = []string{
	"elektrisch bouwmaterieel",
	"fasering van de bouw",
	"intern of extern salderen",
	"emissiearme stalsystemen",
}

// Heuristic deposition bands (INDICATIVE mol N / ha / yr proxy — NOT an AERIUS number, ADR-001).
// Below buildableBand: indicative buildable. Between the two: buildable with mitigation. Above
// permitBand: a permit / further assessment is indicated.
const (
	indicativeBuildableBand = 0.05
	indicativePermitBand    = 0.5
)

// Check produces an INDICATIVE outcome for the candidate site. It is deterministic and gate-free.
func (c *IndicativeChecker) Check(ctx context.Context, inputs json.RawMessage) (core.CheckOutcome, error) {
	var in NitrogenInputs
	if len(inputs) > 0 {
		if err := json.Unmarshal(inputs, &in); err != nil {
			return core.CheckOutcome{}, fmt.Errorf("parse nitrogen inputs: %w", err)
		}
	}

	estimate := indicativeDeposition(in)

	verdict := core.VerdictBuildable
	switch {
	case estimate > indicativePermitBand:
		verdict = core.VerdictPermitRequired
	case estimate > indicativeBuildableBand:
		verdict = core.VerdictBuildableWithMitigation
	}

	headline := fmt.Sprintf(
		"Indicatieve schatting: ~%.2f mol/ha/jr%s. Niet-bindende voorcheck, geen AERIUS Connect-berekening.",
		estimate, areaSuffix(in.Natura2000Area))

	return core.CheckOutcome{
		Indicative: true, // ADR-001: never authoritative
		Result: core.AssessmentResult{
			Headline:  headline,
			Metrics:   map[string]float64{metricDeposition: estimate},
			EngineRef: "", // ADR-001: NOT an authoritative engine output
		},
		Verdict:     verdict,
		Mitigations: mitigationsFor(verdict),
	}, nil
}

// indicativeDeposition is a deterministic heuristic proxy for nitrogen deposition. It is NOT
// physics and NOT AERIUS — it is a coarse triage estimate (ADR-001/012: we never compute the
// authoritative number ourselves). Higher build intensity, homes, commercial floor area, and more
// offsetting routes push the estimate up; greater distance to the Natura 2000 area pushes it down.
func indicativeDeposition(in NitrogenInputs) float64 {
	intensity := in.BuildIntensity
	if intensity <= 0 {
		intensity = 1
	}
	// crude programme size proxy (homes weighted heavier than commercial m²)
	size := float64(in.Homes)*0.002 + float64(in.CommercialM2)*0.000002
	base := (0.05 + size) * intensity
	base += float64(len(in.Routes)) * 0.02 // reliance on offsetting routes raises sensitivity

	// distance attenuation: closer to the area => higher indicative deposition.
	dist := in.DistanceKm
	if dist < 0.1 {
		dist = 0.1
	}
	est := base / dist
	if est < 0 {
		est = 0
	}
	return est
}

// mitigationsFor returns the INDICATIVE mitigation options that fit a verdict (ADR-004: options,
// not guarantees). A clearly-buildable site needs none; a permit-required site surfaces the full
// curated set.
func mitigationsFor(v core.CheckVerdict) []string {
	switch v {
	case core.VerdictBuildable:
		return nil
	case core.VerdictBuildableWithMitigation:
		return append([]string(nil), mitigationOptions[:2]...)
	case core.VerdictPermitRequired:
		return append([]string(nil), mitigationOptions...)
	default:
		return nil
	}
}

func areaSuffix(area string) string {
	if area == "" {
		return ""
	}
	return " bij " + area
}

var _ core.LocationChecker = (*IndicativeChecker)(nil)
