package app

import "github.com/houvast/houvast/internal/core"

// registry is a simple in-memory core.DomainRegistry built at the composition root. It is
// domain-agnostic — it holds whatever Domain implementations are registered (M1: nitrogen only).
type registry struct {
	m map[core.DomainKey]core.Domain
}

// NewRegistry builds a DomainRegistry from the given domains, keyed by Domain.Key().
func NewRegistry(domains ...core.Domain) core.DomainRegistry {
	r := &registry{m: make(map[core.DomainKey]core.Domain, len(domains))}
	for _, d := range domains {
		r.m[d.Key()] = d
	}
	return r
}

func (r *registry) Get(k core.DomainKey) (core.Domain, bool) {
	d, ok := r.m[k]
	return d, ok
}

var _ core.DomainRegistry = (*registry)(nil)
