package nitrogen

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen/nitrogentest"
)

var refTime = time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)

// judge() is pure — these construct the context directly and assert the Finding, no I/O.
func TestJudge_RuleVersion(t *testing.T) {
	th := Threshold{Area: "Veluwe", KDW: 0.10, Unit: "mol/ha/jr"}
	a := core.Assessment{ID: "a1", AssetID: "asset1", TenantID: "t1", Status: core.StatusDefensible}
	e := core.ChangeEvent{ID: "c1", Domain: core.DomainNitrogen, Kind: core.ChangeRuleVersion, Ref: "AERIUS 2025.3"}

	mk := func(old, neu float64) evalContext {
		return evalContext{
			ReferenceTime: refTime,
			PriorResult:   core.AssessmentResult{Metrics: map[string]float64{metricDeposition: old}},
			Recomputed:    &core.AssessmentResult{Metrics: map[string]float64{metricDeposition: neu}},
			Threshold:     &th,
		}
	}

	cases := []struct {
		name     string
		old, neu float64
		want     core.DefensibilityStatus
	}{
		{"over KDW -> exposed", 0.06, 0.11, core.StatusExposed},
		{"near KDW -> attention", 0.06, 0.095, core.StatusAttention},
		{"under KDW -> defensible", 0.06, 0.04, core.StatusDefensible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := judge(a, e, mk(tc.old, tc.neu))
			if f.NewStatus != tc.want {
				t.Fatalf("NewStatus = %q, want %q", f.NewStatus, tc.want)
			}
			if f.Delta == nil || f.Delta.Old != tc.old || f.Delta.New != tc.neu {
				t.Fatalf("Delta = %+v, want %.3f->%.3f", f.Delta, tc.old, tc.neu)
			}
			if !f.EvaluatedAt.Equal(refTime) {
				t.Fatalf("EvaluatedAt = %v, want %v (judge must read the clock from context, not wall time)", f.EvaluatedAt, refTime)
			}
		})
	}
}

func TestJudge_CaseLaw(t *testing.T) {
	scope := CaseLawScope{
		ECLI:           "ECLI:NL:RVS:2024:5178",
		PredicateRoute: "intern_salderen",
		Retroactive:    true,
		EffectiveAt:    time.Date(2024, 12, 18, 0, 0, 0, 0, time.UTC),
		Recommendation: "Vraag een natuurvergunning aan.",
	}
	e := core.ChangeEvent{ID: "c2", Domain: core.DomainNitrogen, Kind: core.ChangeCaseLaw, Ref: scope.ECLI, Summary: "intern salderen vergunningplichtig"}
	a := core.Assessment{ID: "a1", TenantID: "t1", CreatedAt: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Status: core.StatusDefensible}

	mk := func(relies bool) evalContext {
		r := Route{ReliesOn: map[string]bool{}}
		if relies {
			r.ReliesOn["intern_salderen"] = true
		}
		return evalContext{ReferenceTime: refTime, CaseLawScope: &scope, Route: r}
	}

	if f := judge(a, e, mk(true)); f.NewStatus != core.StatusExposed {
		t.Fatalf("relies on route: NewStatus = %q, want exposed", f.NewStatus)
	}
	if f := judge(a, e, mk(false)); f.NewStatus != core.StatusDefensible {
		t.Fatalf("does not rely on route: NewStatus = %q, want defensible", f.NewStatus)
	}
}

// DoD #1 — keep-alive transition end-to-end (assemble + judge) via the fake engine.
func TestEvaluate_KeepAliveTransition(t *testing.T) {
	engine := &nitrogentest.FakeCalculationEngine{
		ResultFunc: func(inputs json.RawMessage, version core.RuleVersionRef) (core.AssessmentResult, error) {
			// Same inputs, NEW version -> higher deposition.
			return core.AssessmentResult{Metrics: map[string]float64{metricDeposition: 0.11}}, nil
		},
	}
	ev := NewImpactEvaluator(
		engine,
		StaticThresholdProvider{Default: &Threshold{Area: "Veluwe", KDW: 0.10, Unit: "mol/ha/jr"}},
		StaticVersionDeltaProvider{Summary: "emissiefactoren gewijzigd"},
		CuratedCaseLawScopeProvider{},
		InputsRouteDeriver{},
		func() time.Time { return refTime },
	)

	inputs, _ := json.Marshal(NitrogenInputs{Natura2000Area: "Veluwe"})
	a := core.Assessment{
		ID: "a1", AssetID: "asset1", TenantID: "t1", Domain: core.DomainNitrogen,
		AuthoredBy:  "Consultancy BV", // ADR-004: the customer authors, never Houvast
		RuleVersion: core.RuleVersionRef{Domain: core.DomainNitrogen, Label: "AERIUS 2024"},
		Inputs:      inputs,
		Result:      core.AssessmentResult{Metrics: map[string]float64{metricDeposition: 0.06}},
		Status:      core.StatusDefensible,
	}
	e := core.ChangeEvent{ID: "c1", Domain: core.DomainNitrogen, Kind: core.ChangeRuleVersion, Ref: "AERIUS 2025.3", EffectiveAt: refTime}

	f, err := ev.Evaluate(context.Background(), a, e)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if f.NewStatus != core.StatusExposed {
		t.Fatalf("NewStatus = %q, want exposed", f.NewStatus)
	}
	if f.Delta == nil || f.Delta.Old != 0.06 || f.Delta.New != 0.11 {
		t.Fatalf("Delta = %+v, want 0.06->0.11", f.Delta)
	}
	if engine.CallCount() != 1 {
		t.Fatalf("engine recompute calls = %d, want 1", engine.CallCount())
	}
}
