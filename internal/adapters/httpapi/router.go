// Package httpapi is the HTTP adapter (driving side of the hexagon): it exposes the application
// services (app.MonitorService, app.CheckService) over JSON and maps DTOs <-> core entities at this
// boundary so the core stays clean (docs/ARCHITECTURE.md).
//
// LAYER BOUNDARY: this package imports ONLY internal/app and internal/core (+ the injected Ingester
// interface). It must NOT import internal/domains/nitrogen or internal/adapters/memory — those
// concrete adapters are wired in cmd/api. Keeping httpapi programmed to ports is what lets a second
// vertical / a Postgres adapter slot in without touching the handlers.
//
// TENANT ISOLATION (ADR-006 — the moat): every request is bound to exactly one tenant, extracted from
// the X-Tenant-ID header by tenantMiddleware. A handler can only ever pass that one TenantID to the
// tenant-scoped app services / repositories; there is no parameter or path that names a different
// tenant, so there is no way to read or write across the tenant boundary via any endpoint. This is
// prove-by-construction isolation at the HTTP edge, mirroring the repository contract.
//
// AUTHN IS A STUB (deferred): the X-Tenant-ID header is a DEVELOPMENT stand-in for real
// authentication. In production the tenant identity MUST come from a verified credential (JWT/OIDC
// claim), never a client-supplied header a caller could forge. Wiring real authn is deferred (see
// docs/DECISIONS.md ADR-006 isolation invariant + the deferred-connectors/authn seam, ADR-010/015).
// Swapping authn in means replacing tenantMiddleware only — the handlers already operate on the
// resolved TenantID and need no change.
package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/worker"
)

// Ingester is the small driving seam for the dev/admin POST /api/ingest endpoint. It lets httpapi
// drive one keep-alive worker cycle WITHOUT depending on the concrete *worker.Worker (or, through it,
// the domain/memory adapters): cmd/api injects the real worker behind this interface. The single
// method mirrors worker.Worker.RunOnce.
type Ingester interface {
	RunOnce(ctx context.Context) (worker.Result, error)
}

// compile-time assertion: the concrete worker satisfies the Ingester seam (it is wired in cmd/api,
// not imported here as a concrete type). Kept here so a signature drift on RunOnce fails the build.
var _ Ingester = (*worker.Worker)(nil)

// Router holds the dependencies the handlers need. All are PORTS / app interfaces (+ the Ingester
// seam + a logger) — no concrete infrastructure.
type router struct {
	monitor  app.MonitorService
	checker  app.CheckService
	ingester Ingester // dev/admin only; may be nil (then /api/ingest returns 503)
	logger   *log.Logger
}

// NewRouter builds the HTTP handler exposing the app services. The Ingester is optional (dev/admin);
// pass nil to disable POST /api/ingest. The logger is used for request/diagnostic logging; if nil a
// no-op logger is used.
func NewRouter(monitor app.MonitorService, checker app.CheckService, ingester Ingester, logger *log.Logger) http.Handler {
	if logger == nil {
		logger = log.New(noopWriter{}, "", 0)
	}
	rt := &router{monitor: monitor, checker: checker, ingester: ingester, logger: logger}

	mux := http.NewServeMux()
	// Go 1.22 method+path patterns. Every handler below is wrapped in tenantMiddleware, so it only
	// ever sees a single resolved TenantID (ADR-006).
	mux.Handle("GET /api/portfolio", rt.tenant(rt.handlePortfolio))
	mux.Handle("GET /api/portfolio/exposure", rt.tenant(rt.handleExposure))
	mux.Handle("GET /api/assessments/{id}/findings", rt.tenant(rt.handleFindings))
	mux.Handle("POST /api/check", rt.tenant(rt.handleCheck))
	mux.Handle("POST /api/promote", rt.tenant(rt.handlePromote))
	mux.Handle("POST /api/ingest", rt.tenant(rt.handleIngest)) // DEV/ADMIN — reproduces the demo flip

	// Health check is intentionally NOT tenant-scoped (no tenant data).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return mux
}

// Routes returns the registered route patterns, for startup logging in cmd/api. Kept in sync with
// NewRouter by hand (the stdlib ServeMux does not expose its patterns).
func Routes() []string {
	return []string{
		"GET  /api/portfolio",
		"GET  /api/portfolio/exposure",
		"GET  /api/assessments/{id}/findings",
		"POST /api/check",
		"POST /api/promote",
		"POST /api/ingest   (dev/admin)",
		"GET  /healthz",
	}
}

// ---- tenant middleware (ADR-006) -------------------------------------------

// tenantHeader is the development stand-in for an authenticated tenant identity (see package doc).
const tenantHeader = "X-Tenant-ID"

// tenantCtxKey is the private context key under which the resolved TenantID is carried.
type tenantCtxKey struct{}

// tenantHandler is a handler that has already had its tenant resolved (ADR-006). It cannot run
// without a TenantID — tenantMiddleware guarantees one is present before dispatch.
type tenantHandler func(w http.ResponseWriter, r *http.Request, t core.TenantID)

// tenant wraps a tenantHandler with the tenant middleware: it extracts and validates X-Tenant-ID and
// rejects the request with 401 if it is missing or empty. Only on success is the handler invoked,
// with the resolved TenantID — so there is no code path where a handler runs un-scoped (ADR-006).
func (rt *router) tenant(h tenantHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get(tenantHeader)
		if raw == "" {
			// 401: no tenant identity. Deliberately generic — do not leak whether the header exists for
			// some tenants, etc. (real authn replaces this; see package doc.)
			writeError(w, http.StatusUnauthorized, "missing or empty "+tenantHeader+" (tenant identity required)")
			return
		}
		t := core.TenantID(raw)
		ctx := context.WithValue(r.Context(), tenantCtxKey{}, t)
		h(w, r.WithContext(ctx), t)
	})
}

// ---- JSON helpers ----------------------------------------------------------

// writeJSON writes v as JSON with the given status. A marshal failure is logged and downgraded to a
// 500 with a generic body (never leak internals — ADR-004 posture extends to error hygiene).
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// errorResponse is the uniform error envelope. The message is a SAFE, caller-facing string — internal
// error detail is logged server-side, never returned (so we don't leak stack/schema/another tenant).
type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// decodeJSON strictly decodes the request body into dst, rejecting unknown fields and trailing data.
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// noopWriter discards log output when NewRouter is given a nil logger.
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
