package memory

import (
	"context"
	"sync"

	"github.com/houvast/houvast/internal/core"
)

// Notification records one NotifyExposure call (tenant + the findings delivered).
type Notification struct {
	Tenant   core.TenantID
	Findings []core.Finding
}

// Notifier is an in-memory core.Notifier that records deliveries for inspection in tests.
// Real delivery (email/webhook/in-app) is deferred.
type Notifier struct {
	mu   sync.Mutex
	Sent []Notification
}

func NewNotifier() *Notifier { return &Notifier{} }

func (n *Notifier) NotifyExposure(ctx context.Context, t core.TenantID, findings []core.Finding) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	cp := make([]core.Finding, len(findings))
	copy(cp, findings)
	n.Sent = append(n.Sent, Notification{Tenant: t, Findings: cp})
	return nil
}

var _ core.Notifier = (*Notifier)(nil)
