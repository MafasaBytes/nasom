// Package memory provides in-memory, tenant-scoped implementations of the core repository
// ports — for the M1 vertical slice and tests. No Postgres yet (deferred, ADR-010).
//
// Tenant isolation (ADR-006) is enforced BY CONSTRUCTION: per-tenant records live in
// per-tenant maps, and no method reads across tenants. A lookup with the wrong TenantID is
// indistinguishable from "not found" — there is no code path that returns another tenant's data.
package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/houvast/houvast/internal/core"
)

// ErrNotFound is returned when a tenant-scoped lookup misses. A record that exists under a
// different tenant also yields ErrNotFound — by design, there is no cross-tenant visibility.
var ErrNotFound = errors.New("memory: not found")

// ---- PortfolioRepository (assets) ------------------------------------------

type PortfolioRepository struct {
	mu       sync.RWMutex
	byTenant map[core.TenantID]map[core.AssetID]core.Asset
}

func NewPortfolioRepository() *PortfolioRepository {
	return &PortfolioRepository{byTenant: map[core.TenantID]map[core.AssetID]core.Asset{}}
}

func (r *PortfolioRepository) SaveAsset(ctx context.Context, a core.Asset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.byTenant[a.TenantID]
	if m == nil {
		m = map[core.AssetID]core.Asset{}
		r.byTenant[a.TenantID] = m
	}
	m[a.ID] = a
	return nil
}

func (r *PortfolioRepository) GetAsset(ctx context.Context, t core.TenantID, id core.AssetID) (core.Asset, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if a, ok := r.byTenant[t][id]; ok {
		return a, nil
	}
	return core.Asset{}, ErrNotFound
}

func (r *PortfolioRepository) ListAssets(ctx context.Context, t core.TenantID) ([]core.Asset, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]core.Asset, 0, len(r.byTenant[t]))
	for _, a := range r.byTenant[t] {
		out = append(out, a)
	}
	return out, nil
}

// ---- AssessmentRepository --------------------------------------------------

type AssessmentRepository struct {
	mu       sync.RWMutex
	byTenant map[core.TenantID]map[core.AssessmentID]core.Assessment
}

func NewAssessmentRepository() *AssessmentRepository {
	return &AssessmentRepository{byTenant: map[core.TenantID]map[core.AssessmentID]core.Assessment{}}
}

func (r *AssessmentRepository) SaveAssessment(ctx context.Context, a core.Assessment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.byTenant[a.TenantID]
	if m == nil {
		m = map[core.AssessmentID]core.Assessment{}
		r.byTenant[a.TenantID] = m
	}
	m[a.ID] = a
	return nil
}

func (r *AssessmentRepository) GetAssessment(ctx context.Context, t core.TenantID, id core.AssessmentID) (core.Assessment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if a, ok := r.byTenant[t][id]; ok {
		return a, nil
	}
	return core.Assessment{}, ErrNotFound
}

func (r *AssessmentRepository) ListByAsset(ctx context.Context, t core.TenantID, asset core.AssetID) ([]core.Assessment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []core.Assessment
	for _, a := range r.byTenant[t] {
		if a.AssetID == asset {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *AssessmentRepository) ListByDomain(ctx context.Context, t core.TenantID, domain core.DomainKey) ([]core.Assessment, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []core.Assessment
	for _, a := range r.byTenant[t] {
		if a.Domain == domain {
			out = append(out, a)
		}
	}
	return out, nil
}

func (r *AssessmentRepository) UpdateStatus(ctx context.Context, t core.TenantID, id core.AssessmentID, status core.DefensibilityStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.byTenant[t]
	a, ok := m[id]
	if !ok {
		return ErrNotFound
	}
	a.Status = status
	m[id] = a
	return nil
}

// TenantsForDomain enumerates the tenant IDs that hold at least one assessment in the domain.
// It returns IDs only — never cross-tenant data — so the "no cross-tenant read" contract
// (ADR-006) holds. It exists to let the monitor fan a GLOBAL change event across tenants while
// processing each tenant in isolation. See ADR-011. Satisfies app.TenantScope structurally.
func (r *AssessmentRepository) TenantsForDomain(ctx context.Context, domain core.DomainKey) ([]core.TenantID, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []core.TenantID
	for t, m := range r.byTenant {
		for _, a := range m {
			if a.Domain == domain {
				out = append(out, t)
				break
			}
		}
	}
	return out, nil
}

// ---- ChangeEventRepository (GLOBAL — change events are not tenant-scoped) ---

type ChangeEventRepository struct {
	mu        sync.RWMutex
	events    map[core.ChangeEventID]core.ChangeEvent
	watermark map[string]int64 // domain|kind -> latest IngestedAt unix nanos
}

func NewChangeEventRepository() *ChangeEventRepository {
	return &ChangeEventRepository{
		events:    map[core.ChangeEventID]core.ChangeEvent{},
		watermark: map[string]int64{},
	}
}

func waterKey(domain core.DomainKey, kind core.ChangeKind) string {
	return string(domain) + "|" + string(kind)
}

func (r *ChangeEventRepository) Save(ctx context.Context, e core.ChangeEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[e.ID] = e
	k := waterKey(e.Domain, e.Kind)
	if ns := e.IngestedAt.UnixNano(); ns > r.watermark[k] {
		r.watermark[k] = ns
	}
	return nil
}

func (r *ChangeEventRepository) LastIngested(ctx context.Context, domain core.DomainKey, kind core.ChangeKind) (time.Time, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ns := r.watermark[waterKey(domain, kind)]
	if ns == 0 {
		return time.Time{}, nil
	}
	return time.Unix(0, ns), nil
}

// ---- FindingRepository (tenant-scoped) -------------------------------------

type FindingRepository struct {
	mu       sync.RWMutex
	byTenant map[core.TenantID][]core.Finding
}

func NewFindingRepository() *FindingRepository {
	return &FindingRepository{byTenant: map[core.TenantID][]core.Finding{}}
}

func (r *FindingRepository) Save(ctx context.Context, f core.Finding) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byTenant[f.TenantID] = append(r.byTenant[f.TenantID], f)
	return nil
}

func (r *FindingRepository) ListByTenant(ctx context.Context, t core.TenantID) ([]core.Finding, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]core.Finding, len(r.byTenant[t]))
	copy(out, r.byTenant[t])
	return out, nil
}

func (r *FindingRepository) ListByAssessment(ctx context.Context, t core.TenantID, id core.AssessmentID) ([]core.Finding, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []core.Finding
	for _, f := range r.byTenant[t] {
		if f.AssessmentID == id {
			out = append(out, f)
		}
	}
	return out, nil
}

// Compile-time port assertions.
var (
	_ core.PortfolioRepository   = (*PortfolioRepository)(nil)
	_ core.AssessmentRepository  = (*AssessmentRepository)(nil)
	_ core.ChangeEventRepository = (*ChangeEventRepository)(nil)
	_ core.FindingRepository     = (*FindingRepository)(nil)
)
