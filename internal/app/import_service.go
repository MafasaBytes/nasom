package app

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/houvast/houvast/internal/core"
)

// houvastAuthor is the forbidden author value (ADR-004): Houvast is a tool, never the adviser of
// record. An imported row authored by "Houvast" (any case) is rejected.
const houvastAuthor = "houvast"

// importedEngineRef marks an AssessmentResult that was IMPORTED (the consultant's own record) rather
// than produced by an authoritative CalculationEngine computation. It is the import analogue of the
// "indicative" sentinel used by Promote (ADR-001/010): it documents provenance — this number is the
// consultant's, not Houvast's computation — and is the seam a later official recompute would replace.
const importedEngineRef = "imported"

type importService struct {
	parser   PortfolioCSVParser
	assets   core.PortfolioRepository
	assess   core.AssessmentRepository
	clock    core.Clock
	idForRow func(externalID string) (core.AssetID, core.AssessmentID)
}

// NewImportService wires the MVP portfolio-import use-case (ADR-010). The PortfolioCSVParser is
// injected (the nitrogen parser is wired in cmd/), so this service — and the whole app layer — never
// imports a domain package. All writes are scoped to the request tenant (ADR-006); the clock is
// injected for deterministic timestamps/testability.
func NewImportService(parser PortfolioCSVParser, assets core.PortfolioRepository, assess core.AssessmentRepository, clock core.Clock) ImportService {
	return &importService{
		parser:   parser,
		assets:   assets,
		assess:   assess,
		clock:    clock,
		idForRow: deterministicIDs,
	}
}

// deterministicIDs derives stable IDs from a row's external_id so re-importing the same portfolio
// UPSERTS rather than duplicating (idempotency, ADR-010). The asset and its assessment share the
// external key; the suffix keeps the two ids distinct within the tenant's stores.
func deterministicIDs(externalID string) (core.AssetID, core.AssessmentID) {
	return core.AssetID("imp-" + externalID), core.AssessmentID("imp-" + externalID + "-assessment")
}

// ImportCSV parses the CSV via the injected domain parser, then persists each parsed row's Asset +
// Assessment scoped to `tenant` (ADR-006). It is gate-free (records the consultant's existing
// assessments; computes nothing) and idempotent (deterministic ids upsert). Per-row problems —
// parse errors from the parser and the author guard / persistence errors here — are collected and a
// bad row is skipped, never aborting the whole import. A fatal parser error (unreadable stream /
// missing header) is returned.
func (s *importService) ImportCSV(ctx context.Context, tenant core.TenantID, r io.Reader) (ImportResult, error) {
	if tenant == "" {
		// Fail closed: an un-scoped import would violate tenant isolation (ADR-006). Callers (httpapi)
		// already reject a missing tenant at the edge; this is defense in depth.
		return ImportResult{}, fmt.Errorf("tenant is required (ADR-006)")
	}

	parsed, err := s.parser.ParseCSV(r)
	if err != nil {
		return ImportResult{}, fmt.Errorf("parse csv: %w", err)
	}

	now := s.clock.Now()
	result := ImportResult{Errors: append([]RowError(nil), parsed.Errors...)}

	for _, row := range parsed.Rows {
		if row.ExternalID == "" {
			result.Errors = append(result.Errors, RowError{Line: row.Line, Reason: "external_id is required (it derives the idempotent id)"})
			continue
		}
		// ADR-004: the consultant/customer is the author of record; reject empty or "Houvast".
		author := strings.TrimSpace(row.AuthoredBy)
		if author == "" {
			result.Errors = append(result.Errors, RowError{Line: row.Line, Reason: "authored_by is required (the consultant of record, ADR-004)"})
			continue
		}
		if strings.EqualFold(author, houvastAuthor) {
			result.Errors = append(result.Errors, RowError{Line: row.Line, Reason: "authored_by must be the consultant of record, never Houvast (ADR-004)"})
			continue
		}

		assetID, assessmentID := s.idForRow(row.ExternalID)

		asset := row.Asset
		asset.ID = assetID
		asset.TenantID = tenant
		if asset.Domain == "" {
			asset.Domain = row.Assessment.Domain
		}
		if asset.Metadata == nil {
			asset.Metadata = map[string]string{}
		}
		if asset.CreatedAt.IsZero() {
			asset.CreatedAt = now
		}
		if err := s.assets.SaveAsset(ctx, asset); err != nil {
			result.Errors = append(result.Errors, RowError{Line: row.Line, Reason: fmt.Sprintf("save asset: %v", err)})
			continue
		}

		assessment := row.Assessment
		assessment.ID = assessmentID
		assessment.AssetID = assetID
		assessment.TenantID = tenant
		assessment.AuthoredBy = author // ADR-004: consultant of record
		// ADR-010: imported assessments enter `defensible` and are then re-evaluated by the monitor on
		// change events like any other. EngineRef = "imported" — their record, not our computation.
		assessment.Status = core.StatusDefensible
		assessment.Result.EngineRef = importedEngineRef
		if assessment.CreatedAt.IsZero() {
			assessment.CreatedAt = now
		}
		if err := s.assess.SaveAssessment(ctx, assessment); err != nil {
			result.Errors = append(result.Errors, RowError{Line: row.Line, Reason: fmt.Sprintf("save assessment: %v", err)})
			continue
		}

		result.Imported++
		result.AssetIDs = append(result.AssetIDs, assetID)
	}

	// Stable error ordering (parser rows already carry line numbers; service-level errors are appended
	// in row order) keeps the response deterministic for tests/UX.
	sort.SliceStable(result.Errors, func(i, j int) bool { return result.Errors[i].Line < result.Errors[j].Line })

	return result, nil
}

var _ ImportService = (*importService)(nil)
