package app_test

// Tests for the MVP CSV portfolio-import use-case (ADR-010). ImportService records a consultant's
// EXISTING, already-authored assessments so the monitor can watch them — it computes nothing. The
// load-bearing behaviours proved here:
//
//   - IDEMPOTENCY: re-importing the same external_id UPSERTS (deterministic ids), never duplicating —
//     asset/assessment counts stay put and ids are stable across imports.
//   - ADR-004 AUTHOR GUARD: a row with empty authored_by OR authored_by == "Houvast" (case-insensitive)
//     is rejected as a RowError with NO asset/assessment written for that row; valid rows in the same
//     import still succeed; AuthoredBy is the consultant, never Houvast.
//   - ADR-006 ISOLATION: everything is written under the request tenant; a different tenant cannot read
//     the imported asset/assessment (GetAsset/GetAssessment/ListByDomain).
//   - ImportResult accounting (Imported count, AssetIDs, Errors) is correct.
//   - The service stamps the cross-cutting fields it owns: tenant, status defensible, EngineRef "imported".

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/houvast/houvast/internal/adapters/memory"
	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen"
)

// importClock is the injected timestamp every imported asset/assessment must carry.
var importClock = time.Date(2026, 6, 13, 9, 0, 0, 0, time.UTC)

const (
	importTenantA = core.TenantID("tenant-vandenberg")
	importTenantB = core.TenantID("tenant-meridiaan")
)

// importHeader is the documented column order (mirrors the nitrogen parser schema).
const importHeader = "external_id,project_name,capital_at_risk_eur,natura2000_area,distance_km,homes,commercial_m2,build_intensity,routes,deposition_mol_ha_yr,rule_version_label,authored_by"

// newImportWorld wires the REAL nitrogen parser + in-memory repos + injected clock. Using the real
// parser keeps the service test faithful to the column->domain mapping the demo depends on.
func newImportWorld(t *testing.T) (app.ImportService, *memory.PortfolioRepository, *memory.AssessmentRepository) {
	t.Helper()
	portfolio := memory.NewPortfolioRepository()
	assessments := memory.NewAssessmentRepository()
	svc := app.NewImportService(nitrogen.NewPortfolioCSVParser(), portfolio, assessments, fixedClock{importClock})
	return svc, portfolio, assessments
}

// csvOf builds a CSV body from the documented header plus the given data rows.
func csvOf(rows ...string) io.Reader {
	return strings.NewReader(importHeader + "\n" + strings.Join(rows, "\n") + "\n")
}

// TestImportCSV_HappyPath_AccountingAndStamping proves a clean import: every valid row becomes a
// tenant-scoped asset + assessment, ImportResult accounting is exact, and the service stamps the
// cross-cutting fields it owns (status defensible, EngineRef "imported", tenant, deterministic id).
func TestImportCSV_HappyPath_AccountingAndStamping(t *testing.T) {
	ctx := context.Background()
	svc, portfolio, assessments := newImportWorld(t)

	rows := []string{
		"vdb-veluwe,Woningbouw Veluwe,4200000,Veluwe,2.1,180,0,1.0,intern_salderen,0.06,AERIUS Calculator 2024,Royal HaskoningDHV",
		"vdb-kade,Kadeproject,7800000,Rijntakken,0.8,0,12000,1.2,extern_salderen,0.09,AERIUS Calculator 2024,Sweco Nederland",
	}
	res, err := svc.ImportCSV(ctx, importTenantA, csvOf(rows...))
	if err != nil {
		t.Fatalf("ImportCSV: %v", err)
	}

	// --- Accounting ---
	if res.Imported != 2 {
		t.Errorf("Imported = %d, want 2", res.Imported)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Errors = %v, want none", res.Errors)
	}
	wantIDs := []core.AssetID{"imp-vdb-veluwe", "imp-vdb-kade"}
	if len(res.AssetIDs) != 2 {
		t.Fatalf("AssetIDs = %v, want 2 ids", res.AssetIDs)
	}
	for i, id := range res.AssetIDs {
		if id != wantIDs[i] {
			t.Errorf("AssetIDs[%d] = %q, want %q (deterministic from external_id)", i, id, wantIDs[i])
		}
	}

	// --- Asset persisted under tenant A, stamped by the service ---
	asset, err := portfolio.GetAsset(ctx, importTenantA, "imp-vdb-veluwe")
	if err != nil {
		t.Fatalf("GetAsset(tenantA): %v", err)
	}
	if asset.TenantID != importTenantA {
		t.Errorf("asset.TenantID = %q, want %q", asset.TenantID, importTenantA)
	}
	if asset.Domain != core.DomainNitrogen {
		t.Errorf("asset.Domain = %q, want nitrogen", asset.Domain)
	}
	if asset.CapitalAtRiskEUR != 4_200_000 {
		t.Errorf("asset.CapitalAtRiskEUR = %d, want 4200000", asset.CapitalAtRiskEUR)
	}
	if !asset.CreatedAt.Equal(importClock) {
		t.Errorf("asset.CreatedAt = %v, want injected clock %v", asset.CreatedAt, importClock)
	}

	// --- Assessment persisted under tenant A, stamped by the service ---
	a, err := assessments.GetAssessment(ctx, importTenantA, "imp-vdb-veluwe-assessment")
	if err != nil {
		t.Fatalf("GetAssessment(tenantA): %v", err)
	}
	if a.TenantID != importTenantA {
		t.Errorf("assessment.TenantID = %q, want %q", a.TenantID, importTenantA)
	}
	if a.AssetID != "imp-vdb-veluwe" {
		t.Errorf("assessment.AssetID = %q, want imp-vdb-veluwe", a.AssetID)
	}
	// ADR-004: the consultant of record is the author, never Houvast.
	if a.AuthoredBy != "Royal HaskoningDHV" {
		t.Errorf("assessment.AuthoredBy = %q, want the consultant", a.AuthoredBy)
	}
	if strings.EqualFold(a.AuthoredBy, "houvast") {
		t.Errorf("ADR-004 VIOLATION: assessment authored by Houvast")
	}
	// ADR-010: imported assessments enter `defensible` with EngineRef "imported" (the consultant's
	// record, not our computation).
	if a.Status != core.StatusDefensible {
		t.Errorf("assessment.Status = %q, want defensible (ADR-010)", a.Status)
	}
	if a.Result.EngineRef != "imported" {
		t.Errorf("assessment.Result.EngineRef = %q, want \"imported\" (ADR-010 provenance)", a.Result.EngineRef)
	}
	if got := a.Result.Metrics["deposition_mol_ha_yr"]; got != 0.06 {
		t.Errorf("deposition metric = %v, want 0.06 (the consultant's number, carried verbatim)", got)
	}
	if a.RuleVersion.Label != "AERIUS Calculator 2024" {
		t.Errorf("RuleVersion.Label = %q, want \"AERIUS Calculator 2024\"", a.RuleVersion.Label)
	}
	if !a.CreatedAt.Equal(importClock) {
		t.Errorf("assessment.CreatedAt = %v, want injected clock %v", a.CreatedAt, importClock)
	}
}

// TestImportCSV_Idempotent proves re-importing the same external_id UPSERTS rather than duplicating:
// counts stay put, ids are stable, and an edited row overwrites the prior record (no second asset).
func TestImportCSV_Idempotent(t *testing.T) {
	ctx := context.Background()
	svc, portfolio, assessments := newImportWorld(t)

	row := "vdb-veluwe,Woningbouw Veluwe,4200000,Veluwe,2.1,180,0,1.0,intern_salderen,0.06,AERIUS Calculator 2024,Royal HaskoningDHV"

	// First import.
	res1, err := svc.ImportCSV(ctx, importTenantA, csvOf(row))
	if err != nil {
		t.Fatalf("first ImportCSV: %v", err)
	}
	if res1.Imported != 1 {
		t.Fatalf("first import Imported = %d, want 1", res1.Imported)
	}

	// Second import of the SAME external_id, with an edited capital + deposition (an updated dossier).
	edited := "vdb-veluwe,Woningbouw Veluwe,5000000,Veluwe,2.1,180,0,1.0,intern_salderen,0.08,AERIUS Calculator 2024,Royal HaskoningDHV"
	res2, err := svc.ImportCSV(ctx, importTenantA, csvOf(edited))
	if err != nil {
		t.Fatalf("second ImportCSV: %v", err)
	}
	if res2.Imported != 1 {
		t.Fatalf("second import Imported = %d, want 1 (upsert, still one row)", res2.Imported)
	}

	// Deterministic ids: both imports touched the same id.
	if res1.AssetIDs[0] != res2.AssetIDs[0] {
		t.Errorf("asset id drifted across imports: %q then %q (must be deterministic)", res1.AssetIDs[0], res2.AssetIDs[0])
	}

	// NO duplication: exactly one asset and one assessment under tenant A.
	assets, err := portfolio.ListAssets(ctx, importTenantA)
	if err != nil {
		t.Fatalf("ListAssets: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("after re-import tenant A has %d assets, want 1 (upsert, no dupe)", len(assets))
	}
	all, err := assessments.ListByDomain(ctx, importTenantA, core.DomainNitrogen)
	if err != nil {
		t.Fatalf("ListByDomain: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("after re-import tenant A has %d assessments, want 1 (upsert, no dupe)", len(all))
	}

	// The upsert took the EDITED values (the record was overwritten, not ignored).
	if assets[0].CapitalAtRiskEUR != 5_000_000 {
		t.Errorf("upsert capital = %d, want the edited 5000000", assets[0].CapitalAtRiskEUR)
	}
	if got := all[0].Result.Metrics["deposition_mol_ha_yr"]; got != 0.08 {
		t.Errorf("upsert deposition = %v, want the edited 0.08", got)
	}
}

// TestImportCSV_AuthorGuard proves the ADR-004 guard at the SERVICE layer: empty author and
// "Houvast" (any case) are rejected as RowErrors with NO write for that row, while valid rows in the
// SAME import still succeed.
func TestImportCSV_AuthorGuard(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name       string
		author     string // the authored_by cell for the bad row
		wantReason string
	}{
		{name: "empty_author_rejected", author: "", wantReason: "authored_by is required"},
		{name: "houvast_exact_rejected", author: "Houvast", wantReason: "never Houvast"},
		{name: "houvast_lowercase_rejected", author: "houvast", wantReason: "never Houvast"},
		{name: "houvast_mixedcase_rejected", author: "HoUvAsT", wantReason: "never Houvast"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, portfolio, assessments := newImportWorld(t)

			// line 2 = a valid row; line 3 = the offending-author row.
			good := "vdb-good,Goed Project,1000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,Sweco Nederland"
			bad := "vdb-bad,Fout Project,2000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024," + tc.author

			res, err := svc.ImportCSV(ctx, importTenantA, csvOf(good, bad))
			if err != nil {
				t.Fatalf("ImportCSV: %v", err)
			}

			// The valid row succeeded; the bad-author row did not.
			if res.Imported != 1 {
				t.Errorf("Imported = %d, want 1 (only the valid row)", res.Imported)
			}
			if len(res.AssetIDs) != 1 || res.AssetIDs[0] != "imp-vdb-good" {
				t.Errorf("AssetIDs = %v, want [imp-vdb-good]", res.AssetIDs)
			}

			// Exactly one RowError, on the bad row's line (line 3), naming the author problem.
			if len(res.Errors) != 1 {
				t.Fatalf("Errors = %v, want exactly 1 (the bad-author row)", res.Errors)
			}
			re := res.Errors[0]
			if re.Line != 3 {
				t.Errorf("RowError.Line = %d, want 3", re.Line)
			}
			if !strings.Contains(re.Reason, tc.wantReason) {
				t.Errorf("RowError.Reason = %q, want it to mention %q", re.Reason, tc.wantReason)
			}

			// NO asset/assessment written for the rejected row (no partial write, ADR-004).
			if _, err := portfolio.GetAsset(ctx, importTenantA, "imp-vdb-bad"); !errors.Is(err, memory.ErrNotFound) {
				t.Errorf("bad-author row created an asset (err=%v); ADR-004 requires no write", err)
			}
			if _, err := assessments.GetAssessment(ctx, importTenantA, "imp-vdb-bad-assessment"); !errors.Is(err, memory.ErrNotFound) {
				t.Errorf("bad-author row created an assessment (err=%v); ADR-004 requires no write", err)
			}

			// The good row IS present and authored by the consultant.
			a, err := assessments.GetAssessment(ctx, importTenantA, "imp-vdb-good-assessment")
			if err != nil {
				t.Fatalf("valid row should have been written: %v", err)
			}
			if a.AuthoredBy != "Sweco Nederland" {
				t.Errorf("valid row AuthoredBy = %q, want the consultant", a.AuthoredBy)
			}
		})
	}
}

// TestImportCSV_TenantIsolation proves ADR-006: every imported record is scoped to the request tenant,
// and a DIFFERENT tenant cannot read the imported asset/assessment by any tenant-scoped path.
func TestImportCSV_TenantIsolation(t *testing.T) {
	ctx := context.Background()
	svc, portfolio, assessments := newImportWorld(t)

	row := "vdb-veluwe,Woningbouw Veluwe,4200000,Veluwe,2.1,180,0,1.0,intern_salderen,0.06,AERIUS Calculator 2024,Royal HaskoningDHV"
	if _, err := svc.ImportCSV(ctx, importTenantA, csvOf(row)); err != nil {
		t.Fatalf("ImportCSV(tenantA): %v", err)
	}

	// Tenant A CAN read its own import (sanity).
	if _, err := portfolio.GetAsset(ctx, importTenantA, "imp-vdb-veluwe"); err != nil {
		t.Fatalf("tenant A cannot read its own import: %v", err)
	}

	// Tenant B must NOT be able to read tenant A's imported asset...
	if _, err := portfolio.GetAsset(ctx, importTenantB, "imp-vdb-veluwe"); !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("ADR-006 LEAK: tenant B read tenant A's imported asset (err=%v, want ErrNotFound)", err)
	}
	// ...nor its assessment by id...
	if _, err := assessments.GetAssessment(ctx, importTenantB, "imp-vdb-veluwe-assessment"); !errors.Is(err, memory.ErrNotFound) {
		t.Errorf("ADR-006 LEAK: tenant B read tenant A's imported assessment (err=%v, want ErrNotFound)", err)
	}
	// ...nor see it in any tenant-B listing.
	if got, _ := portfolio.ListAssets(ctx, importTenantB); len(got) != 0 {
		t.Errorf("ADR-006 LEAK: tenant B ListAssets = %d, want 0", len(got))
	}
	if got, _ := assessments.ListByDomain(ctx, importTenantB, core.DomainNitrogen); len(got) != 0 {
		t.Errorf("ADR-006 LEAK: tenant B ListByDomain = %d, want 0", len(got))
	}
}

// TestImportCSV_EmptyTenantRejected proves the defense-in-depth fail-closed guard (ADR-006): an
// un-scoped import is refused and nothing is parsed or written.
func TestImportCSV_EmptyTenantRejected(t *testing.T) {
	ctx := context.Background()
	svc, portfolio, _ := newImportWorld(t)

	row := "vdb-veluwe,Woningbouw Veluwe,4200000,Veluwe,2.1,180,0,1.0,intern_salderen,0.06,AERIUS Calculator 2024,Royal HaskoningDHV"
	if _, err := svc.ImportCSV(ctx, "", csvOf(row)); err == nil {
		t.Fatal("ImportCSV with empty tenant returned nil error; ADR-006 requires a scoped import")
	}
	// Nothing written under any tenant.
	if got, _ := portfolio.ListAssets(ctx, importTenantA); len(got) != 0 {
		t.Errorf("empty-tenant import wrote %d assets under tenant A, want 0", len(got))
	}
}

// TestImportCSV_FatalParseError proves a structural parse failure (missing header column) is returned
// as an error (a 400 at the HTTP edge), not silently swallowed, and writes nothing.
func TestImportCSV_FatalParseError(t *testing.T) {
	ctx := context.Background()
	svc, portfolio, _ := newImportWorld(t)

	// Header missing the required authored_by column -> fatal parser error.
	badHeader := "external_id,project_name,capital_at_risk_eur,natura2000_area,distance_km,homes,commercial_m2,build_intensity,routes,deposition_mol_ha_yr,rule_version_label"
	body := strings.NewReader(badHeader + "\nx,P,1000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024\n")

	if _, err := svc.ImportCSV(ctx, importTenantA, body); err == nil {
		t.Fatal("ImportCSV with a missing header column returned nil error; want a fatal parse error")
	}
	if got, _ := portfolio.ListAssets(ctx, importTenantA); len(got) != 0 {
		t.Errorf("fatal-parse import wrote %d assets, want 0", len(got))
	}
}

// TestImportCSV_BadAndGoodRowsAccounting proves end-to-end accounting when an import mixes a malformed
// row (parser-level RowError) with valid rows: the good rows import, the bad row is reported, and the
// whole import is NOT aborted. Errors are ordered by line for a deterministic response.
func TestImportCSV_BadAndGoodRowsAccounting(t *testing.T) {
	ctx := context.Background()
	svc, _, assessments := newImportWorld(t)

	rows := []string{
		"vdb-good1,Goed Een,1000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,Sweco",              // line 2 ok
		"vdb-bad,Slecht,2000000,Veluwe,not-a-number,10,0,1.0,intern_salderen,0.07,AERIUS Calculator 2024,Arcadis",       // line 3 bad distance
		"vdb-good2,Goed Twee,3000000,Rijntakken,2.0,20,0,1.0,extern_salderen,0.09,AERIUS Calculator 2024,Witteveen+Bos", // line 4 ok
	}
	res, err := svc.ImportCSV(ctx, importTenantA, csvOf(rows...))
	if err != nil {
		t.Fatalf("ImportCSV: %v", err)
	}

	if res.Imported != 2 {
		t.Errorf("Imported = %d, want 2 (the good rows)", res.Imported)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("Errors = %v, want exactly 1 (the malformed row)", res.Errors)
	}
	if res.Errors[0].Line != 3 {
		t.Errorf("RowError.Line = %d, want 3", res.Errors[0].Line)
	}
	if !strings.Contains(res.Errors[0].Reason, "distance_km") {
		t.Errorf("RowError.Reason = %q, want it to mention distance_km", res.Errors[0].Reason)
	}

	all, _ := assessments.ListByDomain(ctx, importTenantA, core.DomainNitrogen)
	if len(all) != 2 {
		t.Errorf("persisted assessments = %d, want 2 (bad row skipped)", len(all))
	}
}

// TestImportCSV_ErrorOrdering proves service-level row errors are returned sorted by line, so the
// response is deterministic for the UI/tests even when bad rows are interleaved with good ones.
func TestImportCSV_ErrorOrdering(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newImportWorld(t)

	rows := []string{
		"vdb-a,A,1000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,",        // line 2: empty author
		"vdb-b,B,2000000,Veluwe,bad,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,Sweco",   // line 3: bad distance (parser)
		"vdb-c,C,3000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,Houvast", // line 4: forbidden author
	}
	res, err := svc.ImportCSV(ctx, importTenantA, csvOf(rows...))
	if err != nil {
		t.Fatalf("ImportCSV: %v", err)
	}
	if res.Imported != 0 {
		t.Errorf("Imported = %d, want 0 (all three rows are bad)", res.Imported)
	}
	if len(res.Errors) != 3 {
		t.Fatalf("Errors = %v, want 3", res.Errors)
	}
	for i := 1; i < len(res.Errors); i++ {
		if res.Errors[i-1].Line > res.Errors[i].Line {
			t.Errorf("errors not sorted by line: %v", res.Errors)
		}
	}
	wantLines := []int{2, 3, 4}
	for i, e := range res.Errors {
		if e.Line != wantLines[i] {
			t.Errorf("Errors[%d].Line = %d, want %d", i, e.Line, wantLines[i])
		}
	}
}

// --- a tiny fake parser, to prove the service contract independent of the nitrogen parser ----------

// fakeParser returns a fixed ParseResult (or a fatal error), so service-level behaviour can be tested
// without coupling to the nitrogen column schema. It exercises the injected-seam contract directly.
type fakeParser struct {
	result app.ParseResult
	err    error
}

func (p fakeParser) ParseCSV(_ io.Reader) (app.ParseResult, error) { return p.result, p.err }

var _ app.PortfolioCSVParser = fakeParser{}

// TestImportCSV_WithFakeParser_ServiceFillsCrossCuttingFields proves the service contract over the
// injected seam: given parser rows that carry only domain shapes, the service fills tenant +
// deterministic ids + status + EngineRef, and a fatal parser error is propagated (not swallowed).
func TestImportCSV_WithFakeParser_ServiceFillsCrossCuttingFields(t *testing.T) {
	ctx := context.Background()
	portfolio := memory.NewPortfolioRepository()
	assessments := memory.NewAssessmentRepository()

	parser := fakeParser{
		result: app.ParseResult{
			Rows: []app.ParsedRow{
				{
					ExternalID: "ext-1",
					AuthoredBy: "Consultant BV",
					Line:       2,
					Asset:      core.Asset{Domain: core.DomainNitrogen, Name: "Project 1", CapitalAtRiskEUR: 1_000_000},
					Assessment: core.Assessment{Domain: core.DomainNitrogen},
				},
			},
		},
	}
	svc := app.NewImportService(parser, portfolio, assessments, fixedClock{importClock})

	res, err := svc.ImportCSV(ctx, importTenantA, strings.NewReader("ignored by fake"))
	if err != nil {
		t.Fatalf("ImportCSV: %v", err)
	}
	if res.Imported != 1 || len(res.AssetIDs) != 1 {
		t.Fatalf("accounting: Imported=%d AssetIDs=%v, want 1/[imp-ext-1]", res.Imported, res.AssetIDs)
	}

	a, err := assessments.GetAssessment(ctx, importTenantA, "imp-ext-1-assessment")
	if err != nil {
		t.Fatalf("GetAssessment: %v", err)
	}
	// The service stamps these even though the parser left them empty.
	if a.TenantID != importTenantA {
		t.Errorf("TenantID = %q, want %q", a.TenantID, importTenantA)
	}
	if a.Status != core.StatusDefensible {
		t.Errorf("Status = %q, want defensible", a.Status)
	}
	if a.Result.EngineRef != "imported" {
		t.Errorf("EngineRef = %q, want \"imported\"", a.Result.EngineRef)
	}
	if a.AuthoredBy != "Consultant BV" {
		t.Errorf("AuthoredBy = %q, want \"Consultant BV\"", a.AuthoredBy)
	}

	// A FATAL parser error must propagate as a returned error and write nothing.
	failing := app.NewImportService(fakeParser{err: errors.New("boom")}, portfolio, assessments, fixedClock{importClock})
	if _, err := failing.ImportCSV(ctx, importTenantA, strings.NewReader("x")); err == nil {
		t.Fatal("a fatal parser error must propagate, got nil")
	}
}

// TestImportCSV_EmptyExternalID_DefenseInDepth proves the service's own empty-external_id guard (the
// id is the idempotency key). The nitrogen parser already rejects this at parse time, so this exercises
// the SERVICE seam directly via a fake parser that hands back a row with no ExternalID: it must become
// a RowError on the row's line and write nothing, while a sibling valid row still imports.
func TestImportCSV_EmptyExternalID_DefenseInDepth(t *testing.T) {
	ctx := context.Background()
	portfolio := memory.NewPortfolioRepository()
	assessments := memory.NewAssessmentRepository()

	parser := fakeParser{
		result: app.ParseResult{
			Rows: []app.ParsedRow{
				{ExternalID: "", AuthoredBy: "Consultant BV", Line: 2, Asset: core.Asset{Domain: core.DomainNitrogen}, Assessment: core.Assessment{Domain: core.DomainNitrogen}},
				{ExternalID: "ext-ok", AuthoredBy: "Consultant BV", Line: 3, Asset: core.Asset{Domain: core.DomainNitrogen}, Assessment: core.Assessment{Domain: core.DomainNitrogen}},
			},
		},
	}
	svc := app.NewImportService(parser, portfolio, assessments, fixedClock{importClock})

	res, err := svc.ImportCSV(ctx, importTenantA, strings.NewReader("ignored"))
	if err != nil {
		t.Fatalf("ImportCSV: %v", err)
	}
	if res.Imported != 1 {
		t.Errorf("Imported = %d, want 1 (only the row with an external_id)", res.Imported)
	}
	if len(res.Errors) != 1 || res.Errors[0].Line != 2 {
		t.Fatalf("Errors = %v, want one RowError on line 2", res.Errors)
	}
	if !strings.Contains(res.Errors[0].Reason, "external_id is required") {
		t.Errorf("RowError.Reason = %q, want it to mention external_id", res.Errors[0].Reason)
	}
}

// failingPortfolio is a PortfolioRepository whose SaveAsset always fails, to exercise the service's
// per-row persistence-error path (a save error becomes a RowError and is skipped, never aborting).
type failingPortfolio struct{ *memory.PortfolioRepository }

func (failingPortfolio) SaveAsset(context.Context, core.Asset) error {
	return errors.New("disk full")
}

// TestImportCSV_PersistenceError_BecomesRowError proves a per-row persistence failure is collected as a
// RowError (not a fatal error) and the import does not abort — graceful per-row degradation (ADR-010).
func TestImportCSV_PersistenceError_BecomesRowError(t *testing.T) {
	ctx := context.Background()
	assessments := memory.NewAssessmentRepository()

	parser := fakeParser{
		result: app.ParseResult{
			Rows: []app.ParsedRow{
				{ExternalID: "ext-1", AuthoredBy: "Consultant BV", Line: 2, Asset: core.Asset{Domain: core.DomainNitrogen}, Assessment: core.Assessment{Domain: core.DomainNitrogen}},
			},
		},
	}
	svc := app.NewImportService(parser, failingPortfolio{memory.NewPortfolioRepository()}, assessments, fixedClock{importClock})

	res, err := svc.ImportCSV(ctx, importTenantA, strings.NewReader("ignored"))
	if err != nil {
		t.Fatalf("a per-row save failure must NOT be a fatal error, got: %v", err)
	}
	if res.Imported != 0 {
		t.Errorf("Imported = %d, want 0 (the save failed)", res.Imported)
	}
	if len(res.Errors) != 1 || res.Errors[0].Line != 2 {
		t.Fatalf("Errors = %v, want one RowError on line 2", res.Errors)
	}
	if !strings.Contains(res.Errors[0].Reason, "save asset") {
		t.Errorf("RowError.Reason = %q, want it to mention the save failure", res.Errors[0].Reason)
	}
	// The assessment must NOT have been written (the asset save failed first -> row skipped).
	if got, _ := assessments.ListByDomain(ctx, importTenantA, core.DomainNitrogen); len(got) != 0 {
		t.Errorf("assessment written despite asset save failure: %d, want 0", len(got))
	}
}
