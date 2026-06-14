package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
)

// handlePortfolio: GET /api/portfolio — the dashboard read model for the resolved tenant (ADR-006):
// the tenant's projects (asset + latest assessment + latest finding).
func (rt *router) handlePortfolio(w http.ResponseWriter, r *http.Request, t core.TenantID) {
	projects, err := rt.monitor.Portfolio(r.Context(), t)
	if err != nil {
		rt.fail(w, "portfolio", err)
		return
	}
	out := make([]portfolioProjectDTO, 0, len(projects))
	for _, p := range projects {
		out = append(out, toPortfolioProjectDTO(p))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleExposure: GET /api/portfolio/exposure — the dashboard rollup for the resolved tenant.
func (rt *router) handleExposure(w http.ResponseWriter, r *http.Request, t core.TenantID) {
	snap, err := rt.monitor.PortfolioExposure(r.Context(), t)
	if err != nil {
		rt.fail(w, "exposure", err)
		return
	}
	writeJSON(w, http.StatusOK, toExposureSnapshotDTO(snap))
}

// handleFindings: GET /api/assessments/{id}/findings — the change history for one assessment, scoped
// to the resolved tenant (ADR-006: the id is looked up only within the tenant; another tenant's
// assessment id simply yields an empty list, never their data).
func (rt *router) handleFindings(w http.ResponseWriter, r *http.Request, t core.TenantID) {
	id := core.AssessmentID(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing assessment id")
		return
	}
	findings, err := rt.monitor.FindingsForAssessment(r.Context(), t, id)
	if err != nil {
		rt.fail(w, "findings", err)
		return
	}
	out := make([]findingDTO, 0, len(findings))
	for _, f := range findings {
		out = append(out, toFindingDTO(f))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCheck: POST /api/check — the INDICATIVE location pre-check (Surface B, ADR-001). The tenant
// comes from the header (ADR-006), never the body.
func (rt *router) handleCheck(w http.ResponseWriter, r *http.Request, t core.TenantID) {
	var body checkRequestDTO
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	inputs, err := rawInputs(body.Inputs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid inputs")
		return
	}

	res, err := rt.checker.Check(r.Context(), app.CheckRequest{
		Tenant: t,
		Domain: core.DomainKey(body.Domain),
		Inputs: inputs,
	})
	if err != nil {
		rt.fail(w, "check", err)
		return
	}
	writeJSON(w, http.StatusOK, toCheckResultDTO(res))
}

// handlePromote: POST /api/promote — promote a checked site into the tenant's monitored portfolio.
// ADR-004: AuthoredBy is required and comes from the request (the customer/consultant of record),
// never Houvast. The tenant comes from the header (ADR-006), never the body.
func (rt *router) handlePromote(w http.ResponseWriter, r *http.Request, t core.TenantID) {
	var body promoteRequestDTO
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.AuthoredBy == "" {
		// ADR-004: the customer/consultant is the author of record; never default to Houvast.
		writeError(w, http.StatusBadRequest, "authoredBy is required (the customer/consultant of record)")
		return
	}
	inputs, err := rawInputs(body.Inputs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid inputs")
		return
	}

	result := core.AssessmentResult{
		Headline:  body.Result.Headline,
		Metrics:   body.Result.Metrics,
		EngineRef: body.Result.EngineRef,
	}
	assetID, err := rt.checker.Promote(r.Context(), app.CheckRequest{
		Tenant: t,
		Domain: core.DomainKey(body.Domain),
		Inputs: inputs,
	}, body.Name, body.AuthoredBy, result)
	if err != nil {
		rt.fail(w, "promote", err)
		return
	}
	writeJSON(w, http.StatusCreated, promoteResponseDTO{AssetID: string(assetID)})
}

// maxImportBytes caps the CSV import request body (a generous portfolio is well under this) to bound
// memory use on an attacker-controllable upload. Real rate limiting is a prod follow-up (ADR-015).
const maxImportBytes = 8 << 20 // 8 MiB

// handleImport: POST /api/import — CSV portfolio import (ADR-010 MVP cut). The raw request body is a
// CSV (Content-Type text/csv); the tenant comes from the X-Tenant-ID header (ADR-006), never the body,
// so every write is scoped to exactly one tenant. It records the consultant's EXISTING assessments so
// the monitor can watch them — it computes nothing. Per-row problems (bad numbers, missing/forbidden
// author) are returned in the result's `errors`; they do not fail the request. A fatal parse error
// (e.g. missing header) is a 400.
func (rt *router) handleImport(w http.ResponseWriter, r *http.Request, t core.TenantID) {
	if rt.importer == nil {
		writeError(w, http.StatusServiceUnavailable, "import is not enabled")
		return
	}
	defer r.Body.Close()

	// Bound the (attacker-controllable) CSV body so a huge upload can't exhaust memory.
	body := http.MaxBytesReader(w, r.Body, maxImportBytes)

	res, err := rt.importer.ImportCSV(r.Context(), t, body)
	if err != nil {
		// A fatal parse/IO problem (unreadable stream, missing header) is caller-correctable input.
		rt.logger.Printf("httpapi: import failed: %v", err)
		writeError(w, http.StatusBadRequest, "could not parse CSV (check the header row and format)")
		return
	}
	writeJSON(w, http.StatusOK, toImportResultDTO(res))
}

// handleIngest: POST /api/ingest — DEV/ADMIN ONLY. Drives one keep-alive worker cycle (applies the
// curated AERIUS-2025 release + the RvS ruling) and returns the resulting findings + exposure
// snapshots so the UI can reproduce the demo flip. This is NOT a per-tenant endpoint — the worker
// fans the GLOBAL change events across all tenants (ADR-011); the tenant header is still required so
// the endpoint sits behind the same auth stub as the rest of the API.
//
// In production this would be an internal/operator action behind real authz, not a tenant-facing
// route; it is exposed here only so the frontend demo can trigger the flip without the CLI worker.
func (rt *router) handleIngest(w http.ResponseWriter, r *http.Request, _ core.TenantID) {
	if rt.ingester == nil {
		writeError(w, http.StatusServiceUnavailable, "ingest is not enabled")
		return
	}
	res, err := rt.ingester.RunOnce(r.Context())
	if err != nil {
		// A fatal cycle error (e.g. watermark read). Per-event degradation (the gated Connect engine)
		// is NOT fatal — it surfaces in res.Errors below, not here.
		rt.fail(w, "ingest", err)
		return
	}

	out := ingestResponseDTO{
		Events:    make([]changeEventDTO, 0, len(res.Events)),
		Findings:  make([]findingDTO, 0, len(res.Findings)),
		Snapshots: make([]exposureSnapshotDTO, 0, len(res.Snapshots)),
		Errors:    make([]string, 0, len(res.Errors)),
	}
	for _, e := range res.Events {
		out.Events = append(out.Events, toChangeEventDTO(e))
	}
	for _, f := range res.Findings {
		out.Findings = append(out.Findings, toFindingDTO(f))
	}
	for _, s := range res.Snapshots {
		out.Snapshots = append(out.Snapshots, toExposureSnapshotDTO(s))
	}
	// Collected per-event/per-tenant errors are EXPECTED graceful degradation (e.g. the gated AERIUS
	// Connect recompute, ADR-002). Surface their messages for the UI/operator; they are not failures.
	for _, e := range res.Errors {
		out.Errors = append(out.Errors, e.Error())
	}
	writeJSON(w, http.StatusOK, out)
}

// rawInputs re-marshals the decoded JSON `inputs` value back to a json.RawMessage so it can be passed
// opaquely to the domain (core never inspects it). A nil inputs becomes an empty RawMessage (the
// domain treats empty inputs as zero-value, e.g. nitrogen's checker/route deriver).
func rawInputs(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// fail logs the internal error server-side and returns a generic 500 to the caller. Internal detail
// (which could leak schema, another tenant, or stack) is NEVER sent to the client (ADR-004 hygiene).
// Client-correctable input problems are caught earlier in the handlers (400 with a safe message);
// anything reaching here is an unexpected server-side failure, so it is an opaque 500.
func (rt *router) fail(w http.ResponseWriter, op string, err error) {
	rt.logger.Printf("httpapi: %s failed: %v", op, err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
