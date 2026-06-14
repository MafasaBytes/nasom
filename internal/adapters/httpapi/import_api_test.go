package httpapi_test

// HTTP API tests for POST /api/import (CSV portfolio import, ADR-010 MVP cut). These reuse the shared
// harness/helpers in api_test.go (same httpapi_test package): the router is wired exactly as cmd/api,
// with the real nitrogen CSV parser behind app.ImportService.
//
// The load-bearing assertions:
//   - TENANT ISOLATION (ADR-006): missing X-Tenant-ID -> 401; an import is scoped to exactly the header
//     tenant, and the imported projects appear in THAT tenant's /api/portfolio but NEVER another's.
//   - The on-ramp actually FEEDS the monitor: imported projects show up in GET /api/portfolio.
//   - The JSON contract: importResultDTO = {imported, assetIds, errors[{line,reason}]} (camelCase).
//   - A bad-author row surfaces in `errors`, NOT a 5xx (graceful per-row degradation).
//   - A fatal parse error (missing header) is a 400, not a 5xx.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/houvast/houvast/internal/core"
)

// importHeader is the documented CSV column order (mirrors the nitrogen parser schema).
const importHeader = "external_id,project_name,capital_at_risk_eur,natura2000_area,distance_km,homes,commercial_m2,build_intensity,routes,deposition_mol_ha_yr,rule_version_label,authored_by"

// postCSV issues a POST /api/import with a raw text/csv body and the given tenant header (empty tenant
// => no header). It deliberately does NOT JSON-encode the body (unlike the shared `do` helper), because
// the import endpoint consumes a raw CSV stream.
func postCSV(t *testing.T, h *harness, tenant core.TenantID, csv string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(csv))
	req.Header.Set("Content-Type", "text/csv")
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", string(tenant))
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

// csvBody builds a CSV string from the documented header plus the given data rows.
func csvBody(rows ...string) string {
	return importHeader + "\n" + strings.Join(rows, "\n") + "\n"
}

// importVeluwe/importKade are two valid rows under tenants the seed does NOT already own these ids for
// (the import derives imp-* ids, distinct from the seeded asset-* ids), so they are net-new projects.
const (
	importVeluwe = "imp-veluwe,Importproject Veluwe,3300000,Veluwe,2.0,120,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,Tauw"
	importKade   = "imp-kade,Importproject Kade,4400000,Rijntakken,1.0,0,8000,1.1,extern_salderen,0.08,AERIUS Calculator 2024,Tauw"
)

// =====================================================================================
// Req 1 — missing X-Tenant-ID -> 401 (ADR-006), service never reached un-scoped
// =====================================================================================

func TestImport_MissingTenant401(t *testing.T) {
	h := newHarness(t)
	rec := postCSV(t, h, "", csvBody(importVeluwe))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("import with no X-Tenant-ID: want 401, got %d (body %s)", rec.Code, rec.Body.String())
	}
	obj := decodeObject(t, rec)
	if _, ok := obj["error"]; !ok {
		t.Errorf("401 body missing \"error\" envelope: %s", rec.Body.String())
	}
}

// =====================================================================================
// Req 2 — valid CSV -> JSON ImportResult; bad-author row surfaces in errors, not a 5xx
// =====================================================================================

func TestImport_ValidCSV_ContractAndAccounting(t *testing.T) {
	h := newHarness(t)
	rec := postCSV(t, h, tenantA, csvBody(importVeluwe, importKade))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid import: want 200, got %d (body %s)", rec.Code, rec.Body.String())
	}
	obj := decodeObject(t, rec)

	// Contract: importResultDTO = { imported, assetIds, errors }, camelCase, no extras.
	assertKeys(t, "importResult", obj, []string{"imported", "assetIds", "errors"})

	if got := obj["imported"]; got != float64(2) {
		t.Errorf("imported = %v, want 2", got)
	}
	ids, ok := obj["assetIds"].([]any)
	if !ok || len(ids) != 2 {
		t.Fatalf("assetIds = %#v, want a 2-element array", obj["assetIds"])
	}
	if errs, _ := obj["errors"].([]any); len(errs) != 0 {
		t.Errorf("errors = %v, want none on a clean import", errs)
	}
}

func TestImport_BadAuthorRow_SurfacesInErrorsNot5xx(t *testing.T) {
	h := newHarness(t)
	// One valid row + one row authored by "Houvast" (ADR-004 forbidden) + one empty-author row.
	houvast := "imp-bad-houvast,Fout Houvast,1000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,Houvast"
	noAuthor := "imp-bad-empty,Fout Geen Auteur,1000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,"
	rec := postCSV(t, h, tenantA, csvBody(importVeluwe, houvast, noAuthor))

	// Per-row problems are NOT a 5xx — they degrade gracefully into the result's errors.
	if rec.Code != http.StatusOK {
		t.Fatalf("bad-author rows must not 5xx; want 200, got %d (body %s)", rec.Code, rec.Body.String())
	}
	obj := decodeObject(t, rec)
	assertKeys(t, "importResult", obj, []string{"imported", "assetIds", "errors"})

	if got := obj["imported"]; got != float64(1) {
		t.Errorf("imported = %v, want 1 (only the valid row)", got)
	}
	errs, _ := obj["errors"].([]any)
	if len(errs) != 2 {
		t.Fatalf("errors = %v, want 2 (Houvast + empty author)", errs)
	}
	// Each error is a rowError DTO {line, reason} (camelCase) and mentions the author guard.
	for _, e := range errs {
		em, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("error item is not an object: %#v", e)
		}
		assertKeys(t, "rowError", em, []string{"line", "reason"})
		reason, _ := em["reason"].(string)
		if !strings.Contains(strings.ToLower(reason), "author") {
			t.Errorf("error reason = %q, want it to mention the author guard (ADR-004)", reason)
		}
	}
}

func TestImport_FatalParse_400NotPanic(t *testing.T) {
	h := newHarness(t)
	// Header missing the required authored_by column -> fatal parse -> 400 (caller-correctable input).
	badHeader := "external_id,project_name,capital_at_risk_eur,natura2000_area,distance_km,homes,commercial_m2,build_intensity,routes,deposition_mol_ha_yr,rule_version_label"
	body := badHeader + "\nimp-x,P,1000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024\n"
	rec := postCSV(t, h, tenantA, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing header column: want 400, got %d (body %s)", rec.Code, rec.Body.String())
	}
	obj := decodeObject(t, rec)
	if _, ok := obj["error"]; !ok {
		t.Errorf("400 body missing \"error\" envelope: %s", rec.Body.String())
	}
}

// =====================================================================================
// Req 3 — the on-ramp feeds the monitor; cross-tenant isolation END-TO-END (ADR-006)
// =====================================================================================

func TestImport_FeedsMonitor_AndIsTenantIsolatedEndToEnd(t *testing.T) {
	h := newHarness(t)

	// Tenant A imports two NET-NEW projects (imp-* ids, distinct from the 2 seeded asset-* ids).
	rec := postCSV(t, h, tenantA, csvBody(importVeluwe, importKade))
	if rec.Code != http.StatusOK {
		t.Fatalf("import: want 200, got %d (body %s)", rec.Code, rec.Body.String())
	}

	// --- The imported projects now appear in tenant A's portfolio (the monitor watches them) ---
	pa := do(t, h, http.MethodGet, "/api/portfolio", tenantA, nil)
	if pa.Code != http.StatusOK {
		t.Fatalf("portfolio A: want 200, got %d", pa.Code)
	}
	arrA := decodeArray(t, pa)
	// 2 seeded + 2 imported = 4 projects for tenant A.
	if len(arrA) != 4 {
		t.Fatalf("tenant A portfolio after import: want 4 projects (2 seed + 2 import), got %d", len(arrA))
	}
	gotImported := map[string]bool{}
	for _, proj := range arrA {
		asset := proj["asset"].(map[string]any)
		// Every project must belong to tenant A.
		if asset["tenantId"] != string(tenantA) {
			t.Errorf("ADR-006 LEAK: tenant A portfolio contains a %v asset", asset["tenantId"])
		}
		id, _ := asset["id"].(string)
		if id == "imp-imp-veluwe" || id == "imp-imp-kade" {
			gotImported[id] = true
		}
		// The imported assessments must be authored by the consultant (ADR-004), never Houvast, and
		// stamped EngineRef "imported" (ADR-010).
		if la, ok := proj["latestAssessment"].(map[string]any); ok {
			if id == "imp-imp-veluwe" || id == "imp-imp-kade" {
				if la["authoredBy"] != "Tauw" {
					t.Errorf("imported assessment authoredBy = %v, want the consultant \"Tauw\"", la["authoredBy"])
				}
				result, _ := la["result"].(map[string]any)
				if result["engineRef"] != "imported" {
					t.Errorf("imported assessment engineRef = %v, want \"imported\" (ADR-010)", result["engineRef"])
				}
				if la["status"] != "defensible" {
					t.Errorf("imported assessment status = %v, want defensible (ADR-010)", la["status"])
				}
			}
		}
	}
	if !gotImported["imp-imp-veluwe"] || !gotImported["imp-imp-kade"] {
		t.Errorf("imported projects missing from tenant A portfolio: saw %v", gotImported)
	}

	// --- Cross-tenant isolation end-to-end: tenant B must NEVER see A's imported projects ---
	pb := do(t, h, http.MethodGet, "/api/portfolio", tenantB, nil)
	if pb.Code != http.StatusOK {
		t.Fatalf("portfolio B: want 200, got %d", pb.Code)
	}
	arrB := decodeArray(t, pb)
	// Tenant B still has only its 1 seeded asset — the import did not leak into it.
	if len(arrB) != 1 {
		t.Fatalf("ADR-006 LEAK: tenant B portfolio = %d projects, want 1 (its seed only)", len(arrB))
	}
	for _, proj := range arrB {
		asset := proj["asset"].(map[string]any)
		if asset["tenantId"] != string(tenantB) {
			t.Errorf("ADR-006 LEAK: tenant B portfolio contains a %v asset", asset["tenantId"])
		}
	}
	// Defense in depth: none of tenant A's imported ids may appear anywhere in B's portfolio body.
	for _, id := range []string{"imp-imp-veluwe", "imp-imp-kade", "Importproject"} {
		if strings.Contains(pb.Body.String(), id) {
			t.Errorf("ADR-006 LEAK: tenant A's imported content %q appears in tenant B's portfolio", id)
		}
	}
}

// TestImport_ScopedToHeaderTenant proves the import writes under exactly the X-Tenant-ID tenant: the
// SAME CSV imported under B lands only in B's portfolio, never reaching back into A.
func TestImport_ScopedToHeaderTenant(t *testing.T) {
	h := newHarness(t)

	// Import a single project under tenant B.
	if rec := postCSV(t, h, tenantB, csvBody(importVeluwe)); rec.Code != http.StatusOK {
		t.Fatalf("import under B: want 200, got %d (body %s)", rec.Code, rec.Body.String())
	}

	// Tenant B now has its 1 seed + 1 import = 2 projects.
	pb := decodeArray(t, do(t, h, http.MethodGet, "/api/portfolio", tenantB, nil))
	if len(pb) != 2 {
		t.Fatalf("tenant B portfolio after its own import: want 2, got %d", len(pb))
	}

	// Tenant A must be unchanged (still its 2 seeded projects) — the B import did not bleed into A.
	pa := decodeArray(t, do(t, h, http.MethodGet, "/api/portfolio", tenantA, nil))
	if len(pa) != 2 {
		t.Errorf("ADR-006 LEAK: tenant A portfolio = %d, want 2 (unchanged by B's import)", len(pa))
	}
	for _, proj := range pa {
		asset := proj["asset"].(map[string]any)
		if id, _ := asset["id"].(string); id == "imp-imp-veluwe" {
			t.Errorf("ADR-006 LEAK: B's imported project surfaced in tenant A's portfolio")
		}
	}
}
