package nitrogen

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/houvast/houvast/internal/core"
)

// This file holds in-memory, statically-configured implementations of the evaluator's reference
// providers. They are global (the same for every tenant) and wired once from config — NOT
// per-tenant (ADR-009/010). Real ingestion (curated dataset files / DB) is a later swap behind
// these same interfaces.

// StaticThresholdProvider serves KDW thresholds from a fixed map, with an optional default.
type StaticThresholdProvider struct {
	ByArea  map[string]Threshold
	Default *Threshold
}

func (p StaticThresholdProvider) For(ctx context.Context, area string) (Threshold, error) {
	if t, ok := p.ByArea[area]; ok {
		return t, nil
	}
	if p.Default != nil {
		return *p.Default, nil
	}
	return Threshold{}, fmt.Errorf("no KDW threshold configured for area %q", area)
}

// StaticVersionDeltaProvider returns a delta described purely by the two version labels.
type StaticVersionDeltaProvider struct {
	Summary string
}

func (p StaticVersionDeltaProvider) Between(ctx context.Context, from, to core.RuleVersionRef) (VersionDelta, error) {
	return VersionDelta{FromLabel: from.Label, ToLabel: to.Label, Summary: p.Summary}, nil
}

// CuratedCaseLawScopeProvider looks up a ruling's machine-actionable scope by the change event's
// identity (thin events, ADR-009): the event carries the ruling ref; the curated detail is here.
type CuratedCaseLawScopeProvider struct {
	ByRef map[string]CaseLawScope
}

func (p CuratedCaseLawScopeProvider) Scope(ctx context.Context, e core.ChangeEvent) (CaseLawScope, error) {
	if s, ok := p.ByRef[e.Ref]; ok {
		return s, nil
	}
	return CaseLawScope{}, fmt.Errorf("no curated scope for ruling %q", e.Ref)
}

// InputsRouteDeriver derives the doctrinal routes an assessment relies on from its nitrogen
// inputs (NitrogenInputs.Routes).
type InputsRouteDeriver struct{}

func (InputsRouteDeriver) Derive(inputs json.RawMessage) (Route, error) {
	r := Route{ReliesOn: map[string]bool{}}
	if len(inputs) == 0 {
		return r, nil
	}
	var in NitrogenInputs
	if err := json.Unmarshal(inputs, &in); err != nil {
		return Route{}, fmt.Errorf("parse nitrogen inputs: %w", err)
	}
	for _, route := range in.Routes {
		r.ReliesOn[route] = true
	}
	return r, nil
}

// Compile-time assertions that the providers satisfy the evaluator's ports.
var (
	_ ThresholdProvider    = StaticThresholdProvider{}
	_ VersionDeltaProvider = StaticVersionDeltaProvider{}
	_ CaseLawScopeProvider = CuratedCaseLawScopeProvider{}
	_ RouteDeriver         = InputsRouteDeriver{}
)
