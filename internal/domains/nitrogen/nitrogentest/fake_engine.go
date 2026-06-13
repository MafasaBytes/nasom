// Package nitrogentest provides deterministic test doubles for the nitrogen vertical.
// It is importable by both nitrogen tests and app tests, and is NOT part of the production
// server binary. The fake stands in for the official AERIUS Connect API (ADR-001/002) so the
// evaluator's assemble() can "recompute under the new version" with no network and no gate.
package nitrogentest

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/houvast/houvast/internal/core"
)

// Call records one Compute invocation, for assertions.
type Call struct {
	Inputs  json.RawMessage
	Version core.RuleVersionRef
}

// FakeCalculationEngine is a deterministic, injectable core.CalculationEngine. It is driven by
// ResultFunc (a pure function of inputs+version) so a test can make the same assessment yield a
// different result under a new version. If Err is set, Compute returns it (to exercise the
// "Evaluate error leaves status untouched" path). It records all calls in Calls.
type FakeCalculationEngine struct {
	ResultFunc func(inputs json.RawMessage, version core.RuleVersionRef) (core.AssessmentResult, error)
	Err        error

	mu    sync.Mutex
	Calls []Call
}

func (f *FakeCalculationEngine) Compute(ctx context.Context, inputs json.RawMessage, version core.RuleVersionRef) (core.AssessmentResult, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, Call{Inputs: inputs, Version: version})
	f.mu.Unlock()

	if f.Err != nil {
		return core.AssessmentResult{}, f.Err
	}
	if f.ResultFunc != nil {
		return f.ResultFunc(inputs, version)
	}
	return core.AssessmentResult{}, nil
}

func (f *FakeCalculationEngine) Name() string { return "fake-aerius-connect" }

// CallCount returns how many times Compute was invoked.
func (f *FakeCalculationEngine) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}

var _ core.CalculationEngine = (*FakeCalculationEngine)(nil)
