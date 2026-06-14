package nitrogen

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
)

// PortfolioCSVParser is the nitrogen implementation of app.PortfolioCSVParser (ADR-010 MVP cut). It
// owns all nitrogen-specific column knowledge — natura2000_area, deposition_mol_ha_yr, routes, the
// rule-version label — so that knowledge stays in the vertical, never in app/core (ADR-007). It
// parses a documented CSV into core.Asset + core.Assessment shapes; the app.ImportService fills the
// cross-cutting fields (tenant, idempotent ids, status, EngineRef, timestamps).
//
// CSV SCHEMA (first row = header; exact column order below; see testdata/portfolio_sample.csv):
//
//	external_id          stable business key per project (derives the idempotent import id)
//	project_name         human name of the asset
//	capital_at_risk_eur  integer euros at risk (drives €exposure enrichment in the monitor)
//	natura2000_area      the affected Natura 2000 area (e.g. "Veluwe")
//	distance_km          distance to that area, kilometres (float)
//	homes                number of dwellings in the programme (int)
//	commercial_m2        commercial floor area, m² (int)
//	build_intensity      coarse build-intensity factor (float)
//	routes               ;-separated offsetting routes, e.g. "intern_salderen;extern_salderen"
//	deposition_mol_ha_yr the consultant's authored deposition result, mol N / ha / yr (float)
//	rule_version_label   the AERIUS version the row was authored under, e.g. "AERIUS Calculator 2024"
//	authored_by          the consultant/customer of record (ADR-004) — never Houvast
//
// SEMANTICS (ADR-010): this records the consultant's EXISTING, already-authored assessment so the
// monitor can watch it. We compute NOTHING — deposition_mol_ha_yr is the consultant's number, carried
// into AssessmentResult.Metrics["deposition_mol_ha_yr"]. The ImportService stamps EngineRef="imported"
// and status defensible; the row is then re-evaluated by the monitor on change events like any other.
type PortfolioCSVParser struct{}

// NewPortfolioCSVParser builds the gate-free nitrogen CSV parser. It takes no engine (it computes
// nothing) and no per-tenant config (the column schema is global).
func NewPortfolioCSVParser() *PortfolioCSVParser { return &PortfolioCSVParser{} }

// expectedHeader is the documented column order. The parser is column-name driven (it builds an index
// from the header), so column ORDER is tolerant, but every required column must be present.
var importColumns = []string{
	"external_id",
	"project_name",
	"capital_at_risk_eur",
	"natura2000_area",
	"distance_km",
	"homes",
	"commercial_m2",
	"build_intensity",
	"routes",
	"deposition_mol_ha_yr",
	"rule_version_label",
	"authored_by",
}

// ParseCSV reads the portfolio CSV and returns parsed rows + per-row parse errors. A single malformed
// row is SKIPPED with a RowError, never aborting the parse (ADR-010). A non-nil error is reserved for
// a fatal problem (unreadable stream, missing/short header) that prevents parsing at all.
func (p *PortfolioCSVParser) ParseCSV(r io.Reader) (app.ParseResult, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // we validate field counts per-row ourselves (a short row => RowError, not a fatal)
	cr.TrimLeadingSpace = true

	header, err := cr.Read()
	if err != nil {
		return app.ParseResult{}, fmt.Errorf("read header: %w", err)
	}
	idx, err := headerIndex(header)
	if err != nil {
		return app.ParseResult{}, err
	}

	var res app.ParseResult
	line := 1 // header is line 1; first data row is line 2
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			res.Errors = append(res.Errors, app.RowError{Line: line, Reason: fmt.Sprintf("malformed CSV: %v", err)})
			continue
		}
		row, rerr := p.parseRow(idx, rec, line)
		if rerr != nil {
			res.Errors = append(res.Errors, *rerr)
			continue
		}
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}

// headerIndex maps each required column name to its position in the actual header, tolerating column
// reorder but requiring every documented column to be present.
func headerIndex(header []string) (map[string]int, error) {
	pos := make(map[string]int, len(header))
	for i, h := range header {
		pos[strings.TrimSpace(strings.ToLower(h))] = i
	}
	idx := make(map[string]int, len(importColumns))
	var missing []string
	for _, col := range importColumns {
		i, ok := pos[col]
		if !ok {
			missing = append(missing, col)
			continue
		}
		idx[col] = i
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("csv header missing required columns: %s", strings.Join(missing, ", "))
	}
	return idx, nil
}

// parseRow turns one CSV record into an app.ParsedRow (Asset + Assessment), validating numeric
// fields. A bad numeric / missing required field returns a *RowError so the caller skips the row.
func (p *PortfolioCSVParser) parseRow(idx map[string]int, rec []string, line int) (app.ParsedRow, *app.RowError) {
	get := func(col string) string {
		i := idx[col]
		if i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	rowErr := func(reason string) *app.RowError { return &app.RowError{Line: line, Reason: reason} }

	externalID := get("external_id")
	if externalID == "" {
		return app.ParsedRow{}, rowErr("external_id is required")
	}

	capital, err := parseInt(get("capital_at_risk_eur"))
	if err != nil {
		return app.ParsedRow{}, rowErr(fmt.Sprintf("capital_at_risk_eur: %v", err))
	}
	distance, err := parseFloat(get("distance_km"))
	if err != nil {
		return app.ParsedRow{}, rowErr(fmt.Sprintf("distance_km: %v", err))
	}
	homes, err := parseInt(get("homes"))
	if err != nil {
		return app.ParsedRow{}, rowErr(fmt.Sprintf("homes: %v", err))
	}
	commercial, err := parseInt(get("commercial_m2"))
	if err != nil {
		return app.ParsedRow{}, rowErr(fmt.Sprintf("commercial_m2: %v", err))
	}
	intensity, err := parseFloat(get("build_intensity"))
	if err != nil {
		return app.ParsedRow{}, rowErr(fmt.Sprintf("build_intensity: %v", err))
	}
	deposition, err := parseFloat(get("deposition_mol_ha_yr"))
	if err != nil {
		return app.ParsedRow{}, rowErr(fmt.Sprintf("deposition_mol_ha_yr: %v", err))
	}

	area := get("natura2000_area")
	routes := parseRoutes(get("routes"))

	inputs := NitrogenInputs{
		Natura2000Area: area,
		DistanceKm:     distance,
		Homes:          int(homes),
		CommercialM2:   int(commercial),
		BuildIntensity: intensity,
		Routes:         routes,
	}
	rawInputs, merr := json.Marshal(inputs)
	if merr != nil {
		return app.ParsedRow{}, rowErr(fmt.Sprintf("marshal nitrogen inputs: %v", merr))
	}

	asset := core.Asset{
		Domain:           core.DomainNitrogen,
		Name:             get("project_name"),
		CapitalAtRiskEUR: capital,
		Metadata: map[string]string{
			"natura2000":  area,
			"external_id": externalID,
		},
	}

	assessment := core.Assessment{
		Domain: core.DomainNitrogen,
		RuleVersion: core.RuleVersionRef{
			Domain: core.DomainNitrogen,
			Label:  get("rule_version_label"),
		},
		Inputs: rawInputs,
		Result: core.AssessmentResult{
			Headline: fmt.Sprintf("%.2f %s%s", deposition, metricUnit, areaSuffix(area)),
			Metrics:  map[string]float64{metricDeposition: deposition},
			// EngineRef is left empty; the ImportService stamps "imported" (ADR-010) — the consultant's
			// record, not an authoritative engine computation.
		},
		// Status is left zero; the ImportService sets it to defensible (ADR-010).
	}

	return app.ParsedRow{
		Asset:      asset,
		Assessment: assessment,
		ExternalID: externalID,
		AuthoredBy: get("authored_by"),
		Line:       line,
	}, nil
}

// metricUnit is the mol/ha/jr unit shown in the imported headline.
const metricUnit = "mol/ha/jr"

// parseRoutes splits the ;-separated routes column, trimming blanks. An empty cell yields nil routes.
func parseRoutes(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseInt parses an integer field; an empty cell is treated as 0 (optional numeric).
func parseInt(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

// parseFloat parses a float field; an empty cell is treated as 0 (optional numeric).
func parseFloat(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}

// Compile-time assertion that the nitrogen parser satisfies the app seam.
var _ app.PortfolioCSVParser = (*PortfolioCSVParser)(nil)
