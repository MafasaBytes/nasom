package app

import (
	"context"
	"fmt"
	"io"

	"github.com/houvast/houvast/internal/core"
)

// ImportService is the MVP portfolio-ingestion use-case (ADR-010 MVP cut): it records a consultant's
// EXISTING, already-authored assessments so the monitor can watch them. It computes NOTHING — each
// imported row is an assessment the consultant already produced (gate-free; no CalculationEngine call).
//
// LIABILITY POSTURE (ADR-004): every imported assessment is AuthoredBy the consultant/customer of
// record — NEVER Houvast. Imported assessments enter status `defensible` with EngineRef = "imported"
// (their record, not our computation), and are then re-evaluated by the monitor on change events like
// any other assessment.
//
// DEFERRED SEAM (ADR-010): this is a DIRECT use-case, not the generic PortfolioSource/connector
// framework (deferred). Domain-specific column parsing is INJECTED via PortfolioCSVParser, so app does
// not import any domain package.
type ImportService interface {
	// ImportCSV parses a CSV portfolio and persists the resulting assets + assessments, all scoped to
	// the given tenant (ADR-006). It is IDEMPOTENT: re-importing the same external_id upserts rather
	// than duplicating (deterministic IDs derived from external_id). A bad row is skipped and recorded
	// in Errors — it never aborts the whole import. A hard parse/IO error (not row-level) is returned.
	ImportCSV(ctx context.Context, tenant core.TenantID, r io.Reader) (ImportResult, error)
}

// ParsedRow is one successfully-parsed CSV row: an Asset + the Assessment authored against it. The
// parser fills the domain-specific shapes (Inputs, Metrics, RuleVersion, Domain); the service fills
// the cross-cutting fields it owns (TenantID, deterministic IDs, status, EngineRef, timestamps).
type ParsedRow struct {
	Asset      core.Asset
	Assessment core.Assessment
	// ExternalID is the row's stable business key, used by the service to derive deterministic
	// AssetID/AssessmentID so re-import upserts rather than duplicates.
	ExternalID string
	// AuthoredBy is the consultant/customer of record (ADR-004). The service rejects empty/"Houvast".
	AuthoredBy string
	// Line is the 1-based source line (header counts as line 1), so service-level row rejections
	// (e.g. the author guard) can report the same line a parse error would.
	Line int
}

// RowError records why a single CSV row was skipped. Line is 1-based and counts the header (so the
// first data row is line 2), matching what a user sees in a spreadsheet.
type RowError struct {
	Line   int
	Reason string
}

func (e RowError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Reason) }

// ParseResult is what a PortfolioCSVParser returns: the rows it could parse plus per-row parse
// errors. The parser NEVER fails the whole import for a single malformed row — it skips the row and
// records the error. A non-nil returned error is reserved for a fatal problem (e.g. unreadable
// stream / missing header) that prevents parsing at all.
type ParseResult struct {
	Rows   []ParsedRow
	Errors []RowError
}

// PortfolioCSVParser turns a CSV stream into domain-shaped rows. It is the INJECTED seam that keeps
// domain-specific column knowledge (nitrogen: natura2000_area, deposition_mol_ha_yr, routes, ...) out
// of app — the nitrogen implementation lives in internal/domains/nitrogen. app depends only on this
// interface (ADR-007: complexity lives in the vertical; app/core stay domain-agnostic).
type PortfolioCSVParser interface {
	// ParseCSV reads the CSV (first row = header) and returns the parsed rows + per-row parse errors.
	// It MUST NOT abort on a single malformed row — collect a RowError and continue.
	ParseCSV(r io.Reader) (ParseResult, error)
}

// ImportResult is the outcome of an import: how many assessments were recorded, the asset IDs touched,
// and the per-row errors (skipped rows). Imported counts only rows that were actually persisted.
type ImportResult struct {
	Imported int
	AssetIDs []core.AssetID
	Errors   []RowError
}
