// Package worker is the engine-agnostic ingest driver for the keep-alive engine (M2). It runs one
// ingest cycle: poll the configured rule-version sources since the last-ingested watermark, drive
// each emitted ChangeEvent through the monitor's portfolio fan-out (the M1 motion,
// MonitorService.OnChangeEvent), and build a per-tenant exposure snapshot for tenants that had
// findings.
//
// LAYER BOUNDARY (docs/ARCHITECTURE.md): this package is programmed ONLY to the core ports and the
// app services. It must NOT import any domain (nitrogen), adapter (memory), version layer, or test
// double. Composition wires the concrete implementations in cmd/worker. Keeping the driver
// engine-agnostic is what lets a second vertical (PFAS/water/CO2) reuse it unchanged (ADR-007).
//
// The driver is deterministic and unit-testable: it does NO logging and aborts on nothing per-event.
// It returns data (Result) and collects per-event/per-tenant errors; the cmd is responsible for
// logging — including surfacing the gated-engine "Connect gated" outcome as expected graceful
// degradation (ADR-002), not a crash.
package worker

import (
	"context"
	"fmt"

	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
)

// Result is the outcome of one RunOnce cycle. It is pure data so the caller (cmd) decides how to log
// it. Errors are collected, not fatal: a single bad event or tenant must not sink the whole cycle.
type Result struct {
	// Events are every ChangeEvent emitted by the polled sources this cycle (in poll order).
	Events []core.ChangeEvent
	// Findings are the aggregate findings produced across all events and tenants (the unit of value).
	Findings []core.Finding
	// Snapshots is the post-ingest exposure snapshot per tenant that had at least one finding,
	// keyed by TenantID. Built via MonitorService.PortfolioExposure.
	Snapshots map[core.TenantID]core.ExposureSnapshot
	// Errors are the per-event / per-tenant errors collected during the cycle. A non-empty Errors
	// with a nil RunOnce error means "the cycle completed but some work degraded" (e.g. the gated
	// engine surfaced ErrConnectGated per assessment — see ADR-002).
	Errors []error
}

// Worker runs one ingest cycle. It is constructed with the global rule-version source(s), the change
// event repository (for the `since` watermark via LastIngested), the monitor service (the M1 fan-out
// motion), and the tenant scope (to enumerate which tenants to snapshot after ingest).
//
// It holds only core ports + app types — no concrete infrastructure.
type Worker struct {
	sources []core.RuleVersionSource
	changes core.ChangeEventRepository
	monitor app.MonitorService
	tenants app.TenantScope
	// domain is the domain whose rule-version watermark this worker advances. M2 ships nitrogen only
	// (ADR-007); a multi-domain worker is a later, additive change. It is a core.DomainKey constant,
	// not a domain-package import, so the layer boundary holds.
	domain core.DomainKey
}

// New constructs a Worker. The sources are the global RuleVersionSource(s) (e.g. the AERIUS release
// watcher) — change events are global, not tenant-scoped (ADR-011); composition injects them. The
// domain names the vertical whose rule-version watermark this worker advances (core.DomainNitrogen
// in M2).
func New(domain core.DomainKey, sources []core.RuleVersionSource, changes core.ChangeEventRepository, monitor app.MonitorService, tenants app.TenantScope) *Worker {
	return &Worker{
		sources: sources,
		changes: changes,
		monitor: monitor,
		tenants: tenants,
		domain:  domain,
	}
}

// RunOnce runs a single ingest cycle:
//  1. read the last-ingested watermark for (domain, rule_version) from the change repository;
//  2. for each source, Poll(ctx, since) since that watermark;
//  3. for each emitted ChangeEvent, drive MonitorService.OnChangeEvent (the M1 portfolio fan-out,
//     which itself persists the event, evaluates per tenant in isolation, persists findings, updates
//     statuses, and notifies) — collecting (not aborting on) per-event errors;
//  4. aggregate the returned findings and record which tenants had findings;
//  5. build a per-tenant ExposureSnapshot (via MonitorService.PortfolioExposure) for exactly those
//     tenants.
//
// It returns the aggregate Result. The returned error is non-nil only on a watermark-read failure
// (the one thing that prevents the cycle from running coherently); per-event and per-tenant failures
// are collected in Result.Errors so the cycle still completes and surfaces partial value. The gated
// AERIUS Connect engine surfaces ErrConnectGated through OnChangeEvent's collected errors here — the
// monitor leaves status untouched (ADR-002/004); cmd logs it as expected graceful degradation.
func (w *Worker) RunOnce(ctx context.Context) (Result, error) {
	res := Result{Snapshots: map[core.TenantID]core.ExposureSnapshot{}}

	// (1) Watermark for incremental polling. A read failure here is fatal to the cycle: polling from
	// an unknown `since` would risk reprocessing or skipping; surface it so the caller can retry.
	since, err := w.changes.LastIngested(ctx, w.domain, core.ChangeRuleVersion)
	if err != nil {
		return res, fmt.Errorf("read last-ingested watermark for %s/%s: %w", w.domain, core.ChangeRuleVersion, err)
	}

	// affected tracks tenants that produced at least one finding this cycle (set semantics).
	affected := map[core.TenantID]struct{}{}

	// (2)+(3) Poll each source and drive each emitted event through the monitor fan-out.
	for _, src := range w.sources {
		events, err := src.Poll(ctx, since)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("poll rule-version source: %w", err))
			continue // a bad source must not stop the others
		}
		for _, e := range events {
			res.Events = append(res.Events, e)
			findings, err := w.monitor.OnChangeEvent(ctx, e)
			if err != nil {
				// Collected, never fatal: the gated engine surfaces ErrConnectGated per assessment
				// here, and the monitor has already left those statuses untouched (ADR-002/004).
				res.Errors = append(res.Errors, fmt.Errorf("ingest event %s (%s): %w", e.Ref, e.ID, err))
			}
			for _, f := range findings {
				res.Findings = append(res.Findings, f)
				affected[f.TenantID] = struct{}{}
			}
		}
	}

	// (4)+(5) Build a post-ingest exposure snapshot for tenants that had findings. We restrict to
	// affected tenants (not every tenant in scope) so the cycle's output is the changed surface; the
	// TenantScope is still consulted to keep enumeration on the audited, IDs-only path (ADR-011).
	if len(affected) > 0 {
		inScope, err := w.tenants.TenantsForDomain(ctx, w.domain)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("enumerate tenants for %s: %w", w.domain, err))
		} else {
			scoped := make(map[core.TenantID]struct{}, len(inScope))
			for _, t := range inScope {
				scoped[t] = struct{}{}
			}
			for t := range affected {
				if _, ok := scoped[t]; !ok {
					continue // a tenant with findings must also be in domain scope; skip defensively
				}
				snap, err := w.monitor.PortfolioExposure(ctx, t)
				if err != nil {
					res.Errors = append(res.Errors, fmt.Errorf("exposure snapshot for tenant %s: %w", t, err))
					continue
				}
				res.Snapshots[t] = snap
			}
		}
	}

	return res, nil
}
