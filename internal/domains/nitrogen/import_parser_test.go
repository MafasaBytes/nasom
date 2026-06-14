package nitrogen_test

// Tests for the nitrogen CSV portfolio parser (ADR-010 MVP cut). The parser owns all nitrogen-specific
// column knowledge and turns a documented CSV into core.Asset + core.Assessment shapes. The load-bearing
// behaviours proved here:
//
//   - GOOD rows map every column correctly: NitrogenInputs (area, distance, homes, m2, intensity,
//     ;-split routes), Result.Metrics["deposition_mol_ha_yr"], RuleVersion.Label, Domain nitrogen,
//     and the parser leaves Status/EngineRef to the service (it computes nothing).
//   - A BAD row (malformed number / missing required cell) yields a RowError on the right line AND the
//     parse CONTINUES — a single bad row never aborts the import (ADR-010).
//   - A missing/garbled header or an unreadable stream is a FATAL error (returned, not a RowError).

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
	"github.com/houvast/houvast/internal/domains/nitrogen"
)

const depositionMetric = "deposition_mol_ha_yr"

// header is the documented column order, reused by inline-CSV cases below.
const header = "external_id,project_name,capital_at_risk_eur,natura2000_area,distance_km,homes,commercial_m2,build_intensity,routes,deposition_mol_ha_yr,rule_version_label,authored_by"

// parseInputs unmarshals a parsed assessment's opaque Inputs back into NitrogenInputs for assertion.
func parseInputs(t *testing.T, raw json.RawMessage) nitrogen.NitrogenInputs {
	t.Helper()
	var in nitrogen.NitrogenInputs
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("unmarshal nitrogen inputs: %v (raw=%s)", err, raw)
	}
	return in
}

// TestParseCSV_GoodRows asserts the full column->domain mapping for well-formed rows, table-driven over
// the distinguishing fields. Each case is a single data row appended to the documented header.
func TestParseCSV_GoodRows(t *testing.T) {
	cases := []struct {
		name        string
		row         string
		wantExtID   string
		wantAuthor  string
		wantName    string
		wantCapital int64
		wantInputs  nitrogen.NitrogenInputs
		wantDep     float64
		wantLabel   string
	}{
		{
			name:        "single_route_intern_salderen",
			row:         "vdb-veluwe,Woningbouw Veluwe,4200000,Veluwe,2.1,180,0,1.0,intern_salderen,0.06,AERIUS Calculator 2024,Royal HaskoningDHV",
			wantExtID:   "vdb-veluwe",
			wantAuthor:  "Royal HaskoningDHV",
			wantName:    "Woningbouw Veluwe",
			wantCapital: 4_200_000,
			wantInputs:  nitrogen.NitrogenInputs{Natura2000Area: "Veluwe", DistanceKm: 2.1, Homes: 180, CommercialM2: 0, BuildIntensity: 1.0, Routes: []string{"intern_salderen"}},
			wantDep:     0.06,
			wantLabel:   "AERIUS Calculator 2024",
		},
		{
			name:        "multi_route_semicolon_split",
			row:         "vdb-nieuwkoop,Erfontwikkeling,2600000,Nieuwkoopse Plassen,3.4,42,1500,0.8,intern_salderen;extern_salderen,0.04,AERIUS Calculator 2024,Sweco Nederland",
			wantExtID:   "vdb-nieuwkoop",
			wantAuthor:  "Sweco Nederland",
			wantName:    "Erfontwikkeling",
			wantCapital: 2_600_000,
			wantInputs:  nitrogen.NitrogenInputs{Natura2000Area: "Nieuwkoopse Plassen", DistanceKm: 3.4, Homes: 42, CommercialM2: 1500, BuildIntensity: 0.8, Routes: []string{"intern_salderen", "extern_salderen"}},
			wantDep:     0.04,
			wantLabel:   "AERIUS Calculator 2024",
		},
		{
			name:        "empty_routes_cell_yields_nil_routes",
			row:         "vdb-kade,Kadeproject,7800000,Rijntakken,0.8,0,12000,1.2,,0.09,AERIUS Calculator 2025,Arcadis Nederland",
			wantExtID:   "vdb-kade",
			wantAuthor:  "Arcadis Nederland",
			wantName:    "Kadeproject",
			wantCapital: 7_800_000,
			wantInputs:  nitrogen.NitrogenInputs{Natura2000Area: "Rijntakken", DistanceKm: 0.8, Homes: 0, CommercialM2: 12000, BuildIntensity: 1.2, Routes: nil},
			wantDep:     0.09,
			wantLabel:   "AERIUS Calculator 2025",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := nitrogen.NewPortfolioCSVParser()
			res, err := p.ParseCSV(strings.NewReader(header + "\n" + tc.row + "\n"))
			if err != nil {
				t.Fatalf("%s: fatal parse error on a good row: %v", tc.name, err)
			}
			if len(res.Errors) != 0 {
				t.Fatalf("%s: unexpected row errors: %v", tc.name, res.Errors)
			}
			if len(res.Rows) != 1 {
				t.Fatalf("%s: want 1 parsed row, got %d", tc.name, len(res.Rows))
			}
			row := res.Rows[0]

			if row.ExternalID != tc.wantExtID {
				t.Errorf("%s: ExternalID = %q, want %q", tc.name, row.ExternalID, tc.wantExtID)
			}
			if row.AuthoredBy != tc.wantAuthor {
				t.Errorf("%s: AuthoredBy = %q, want %q", tc.name, row.AuthoredBy, tc.wantAuthor)
			}
			if row.Line != 2 { // header is line 1; the single data row is line 2
				t.Errorf("%s: Line = %d, want 2", tc.name, row.Line)
			}

			// --- Asset mapping ---
			if row.Asset.Domain != core.DomainNitrogen {
				t.Errorf("%s: asset.Domain = %q, want %q", tc.name, row.Asset.Domain, core.DomainNitrogen)
			}
			if row.Asset.Name != tc.wantName {
				t.Errorf("%s: asset.Name = %q, want %q", tc.name, row.Asset.Name, tc.wantName)
			}
			if row.Asset.CapitalAtRiskEUR != tc.wantCapital {
				t.Errorf("%s: asset.CapitalAtRiskEUR = %d, want %d", tc.name, row.Asset.CapitalAtRiskEUR, tc.wantCapital)
			}
			if got := row.Asset.Metadata["natura2000"]; got != tc.wantInputs.Natura2000Area {
				t.Errorf("%s: asset.Metadata[natura2000] = %q, want %q", tc.name, got, tc.wantInputs.Natura2000Area)
			}
			if got := row.Asset.Metadata["external_id"]; got != tc.wantExtID {
				t.Errorf("%s: asset.Metadata[external_id] = %q, want %q", tc.name, got, tc.wantExtID)
			}

			// --- Assessment mapping ---
			a := row.Assessment
			if a.Domain != core.DomainNitrogen {
				t.Errorf("%s: assessment.Domain = %q, want %q", tc.name, a.Domain, core.DomainNitrogen)
			}
			if a.RuleVersion.Label != tc.wantLabel {
				t.Errorf("%s: RuleVersion.Label = %q, want %q", tc.name, a.RuleVersion.Label, tc.wantLabel)
			}
			if a.RuleVersion.Domain != core.DomainNitrogen {
				t.Errorf("%s: RuleVersion.Domain = %q, want %q", tc.name, a.RuleVersion.Domain, core.DomainNitrogen)
			}
			// deposition is carried verbatim into the deposition metric (the parser computes nothing).
			if got := a.Result.Metrics[depositionMetric]; got != tc.wantDep {
				t.Errorf("%s: Metrics[%s] = %v, want %v", tc.name, depositionMetric, got, tc.wantDep)
			}
			if a.Result.Headline == "" {
				t.Errorf("%s: assessment headline is empty", tc.name)
			}
			// The parser leaves Status zero and EngineRef empty — those are the service's to stamp (ADR-010).
			if a.Status != "" {
				t.Errorf("%s: parser set Status = %q, want empty (the service stamps it)", tc.name, a.Status)
			}
			if a.Result.EngineRef != "" {
				t.Errorf("%s: parser set EngineRef = %q, want empty (the service stamps \"imported\")", tc.name, a.Result.EngineRef)
			}

			// --- NitrogenInputs round-trip ---
			in := parseInputs(t, a.Inputs)
			if in.Natura2000Area != tc.wantInputs.Natura2000Area {
				t.Errorf("%s: inputs.Natura2000Area = %q, want %q", tc.name, in.Natura2000Area, tc.wantInputs.Natura2000Area)
			}
			if in.DistanceKm != tc.wantInputs.DistanceKm {
				t.Errorf("%s: inputs.DistanceKm = %v, want %v", tc.name, in.DistanceKm, tc.wantInputs.DistanceKm)
			}
			if in.Homes != tc.wantInputs.Homes {
				t.Errorf("%s: inputs.Homes = %d, want %d", tc.name, in.Homes, tc.wantInputs.Homes)
			}
			if in.CommercialM2 != tc.wantInputs.CommercialM2 {
				t.Errorf("%s: inputs.CommercialM2 = %d, want %d", tc.name, in.CommercialM2, tc.wantInputs.CommercialM2)
			}
			if in.BuildIntensity != tc.wantInputs.BuildIntensity {
				t.Errorf("%s: inputs.BuildIntensity = %v, want %v", tc.name, in.BuildIntensity, tc.wantInputs.BuildIntensity)
			}
			if !equalStrings(in.Routes, tc.wantInputs.Routes) {
				t.Errorf("%s: inputs.Routes = %v, want %v", tc.name, in.Routes, tc.wantInputs.Routes)
			}
		})
	}
}

// TestParseCSV_BadRowsAreSkippedNotFatal proves a single malformed row becomes a RowError on the right
// line, the GOOD rows around it still parse, and the parser NEVER hard-fails for a row-level problem.
func TestParseCSV_BadRowsAreSkippedNotFatal(t *testing.T) {
	cases := []struct {
		name       string
		badRow     string
		wantReason string // substring expected in the RowError reason
	}{
		{
			name:       "malformed_distance_km",
			badRow:     "bad-dist,Bad Distance,1000000,Veluwe,not-a-number,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,Sweco",
			wantReason: "distance_km",
		},
		{
			name:       "malformed_deposition",
			badRow:     "bad-dep,Bad Deposition,1000000,Veluwe,1.0,10,0,1.0,intern_salderen,zero,AERIUS Calculator 2024,Sweco",
			wantReason: "deposition_mol_ha_yr",
		},
		{
			name:       "malformed_capital",
			badRow:     "bad-cap,Bad Capital,1.5M,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,Sweco",
			wantReason: "capital_at_risk_eur",
		},
		{
			name:       "missing_required_external_id",
			badRow:     ",No External ID,1000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,Sweco",
			wantReason: "external_id is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// good row on line 2, bad row on line 3, another good row on line 4.
			good1 := "good-1,Good One,1000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024,Sweco"
			good2 := "good-2,Good Two,2000000,Rijntakken,2.0,20,0,1.0,extern_salderen,0.07,AERIUS Calculator 2024,Arcadis"
			csv := strings.Join([]string{header, good1, tc.badRow, good2}, "\n") + "\n"

			p := nitrogen.NewPortfolioCSVParser()
			res, err := p.ParseCSV(strings.NewReader(csv))
			if err != nil {
				t.Fatalf("%s: a bad ROW must not be a fatal error, got: %v", tc.name, err)
			}

			// Parsing continued: both good rows survived.
			if len(res.Rows) != 2 {
				t.Fatalf("%s: want 2 good rows preserved around the bad one, got %d", tc.name, len(res.Rows))
			}
			if res.Rows[0].ExternalID != "good-1" || res.Rows[1].ExternalID != "good-2" {
				t.Errorf("%s: good rows = [%q,%q], want [good-1,good-2]", tc.name, res.Rows[0].ExternalID, res.Rows[1].ExternalID)
			}

			// Exactly one RowError, on line 3 (the bad row), with the expected reason.
			if len(res.Errors) != 1 {
				t.Fatalf("%s: want exactly 1 RowError, got %d: %v", tc.name, len(res.Errors), res.Errors)
			}
			re := res.Errors[0]
			if re.Line != 3 {
				t.Errorf("%s: RowError.Line = %d, want 3 (the bad row)", tc.name, re.Line)
			}
			if !strings.Contains(re.Reason, tc.wantReason) {
				t.Errorf("%s: RowError.Reason = %q, want it to mention %q", tc.name, re.Reason, tc.wantReason)
			}
		})
	}
}

// TestParseCSV_FatalErrors proves a structural problem that prevents parsing AT ALL is a returned error
// (not a RowError): a missing required header column, and an unreadable stream.
func TestParseCSV_FatalErrors(t *testing.T) {
	t.Run("missing_required_header_column", func(t *testing.T) {
		// Drop authored_by from the header — a required column is missing.
		badHeader := "external_id,project_name,capital_at_risk_eur,natura2000_area,distance_km,homes,commercial_m2,build_intensity,routes,deposition_mol_ha_yr,rule_version_label"
		row := "x,Project,1000000,Veluwe,1.0,10,0,1.0,intern_salderen,0.05,AERIUS Calculator 2024"
		p := nitrogen.NewPortfolioCSVParser()
		res, err := p.ParseCSV(strings.NewReader(badHeader + "\n" + row + "\n"))
		if err == nil {
			t.Fatalf("missing header column must be a fatal error; got rows=%v errors=%v", res.Rows, res.Errors)
		}
		if !strings.Contains(err.Error(), "authored_by") {
			t.Errorf("fatal error should name the missing column, got: %v", err)
		}
	})

	t.Run("empty_stream_is_fatal", func(t *testing.T) {
		// An empty stream has no header row at all -> a fatal read-header error (EOF).
		p := nitrogen.NewPortfolioCSVParser()
		if _, err := p.ParseCSV(strings.NewReader("")); err == nil {
			t.Fatal("empty stream (no header) must be a fatal error")
		}
	})

	t.Run("unreadable_stream_is_fatal", func(t *testing.T) {
		p := nitrogen.NewPortfolioCSVParser()
		if _, err := p.ParseCSV(errReader{}); err == nil {
			t.Fatal("an unreadable stream must be a fatal error")
		}
	})
}

// TestParseCSV_TestdataSample parses the committed sample CSV and asserts the documented shape: three
// good rows + one RowError for the deliberately author-less last row (line 5). This guards the fixture
// the demo/import flow depends on.
func TestParseCSV_TestdataSample(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "portfolio_sample.csv"))
	if err != nil {
		t.Fatalf("open sample: %v", err)
	}
	defer f.Close()

	p := nitrogen.NewPortfolioCSVParser()
	res, err := p.ParseCSV(f)
	if err != nil {
		t.Fatalf("ParseCSV(sample): %v", err)
	}

	// All four data rows parse at the parser level (the author-less row has a valid external_id and
	// numbers; the EMPTY-author rejection is the SERVICE's ADR-004 guard, not the parser's job).
	if len(res.Rows) != 4 {
		t.Fatalf("sample: want 4 parsed rows, got %d", len(res.Rows))
	}
	if len(res.Errors) != 0 {
		t.Fatalf("sample: parser should report no row errors (author guard is service-level), got %v", res.Errors)
	}
	// The last row's AuthoredBy is empty (the sample's deliberate ADR-004 case for the service tests).
	last := res.Rows[3]
	if last.ExternalID != "vdb-missing-author" {
		t.Errorf("sample: last row ExternalID = %q, want vdb-missing-author", last.ExternalID)
	}
	if last.AuthoredBy != "" {
		t.Errorf("sample: last row AuthoredBy = %q, want empty (the service rejects it, ADR-004)", last.AuthoredBy)
	}
	// Compile-time guard that the parser satisfies the app seam.
	var _ app.PortfolioCSVParser = p
}

// ---- helpers ---------------------------------------------------------------

// errReader always fails, simulating an unreadable stream.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom: cannot read stream") }

var _ io.Reader = errReader{}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
