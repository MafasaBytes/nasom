package version

import "time"

// This file embeds the curated AERIUS release data. EVERY value here traces to the
// regulatory-caselaw-analyst's cited note: docs/regulatory/aerius-2025-release.md (curated
// 2026-06-13). Primary-source URLs are carried on each record (changelog/audit, ADR-004).
//
// Reminder of the hard boundaries this file honours:
//   - No physics: metadata / narrative / schema-version / COARSE direction only (ADR-001/009).
//   - Direction-of-impact fields are TRIAGE/PRIORITISATION ONLY; they never set or skip a status.
//   - IMAER exact version for the Oct-2025 build is UNCONFIRMED -> ImaerExactConfirmed: false (§6).

// date is a small helper for readable literal dates (UTC).
func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// curatedOn / curatedBy are the shared provenance stamp for this changelog batch.
var (
	curatedOn = date(2026, 6, 13)
	curatedBy = "regulatory-caselaw-analyst"
)

// sources2025 are the primary URLs backing the 2025 release + the 2024->2025 delta.
// Source list: docs/regulatory/aerius-2025-release.md §7 (all consulted 2026-06-13).
var sources2025 = []string{
	"https://www.rivm.nl/publicaties/actualisatie-aerius-calculator-2025", // RIVM briefrapport 2025-0020
	"https://www.aeriusproducten.nl/actueel/nieuws/2025/10/7/aerius-calculator-en-monitor-geactualiseerd",
	"https://www.aeriusproducten.nl/actueel/nieuws/2025/7/7/actualisatie-aerius-calculator-en-monitor-op-7-oktober-2025",
	"https://www.aeriusproducten.nl/actueel/nieuws/2025/9/4/meer-informatie-over-de-actualisatie-van-aerius-calculator-connect-en-monitor-op-7-oktober-2025",
	"https://register.geostandaarden.nl/informatiemodel/imaer/",
	"https://bmdadvies.nl/blogs/invloed-van-versiewijziging-aerius-calculator/",
	"https://stikstofinfo.net/2025/10/07/aerius-update-2025-een-overzicht-van-de-belangrijkste-technische-wijzigingen-deel-1/",
}

// release2024 — the prior mandatory annual release that 2025 supersedes.
// IMAER 6.0.0 (2024-10-01) aligns with AERIUS 2024. Source: §2 geostandaarden register.
var release2024 = AeriusRelease{
	Label:      "AERIUS Calculator 2024",
	VersionKey: "2024",
	Products:   []string{"Calculator", "Connect", "Monitor"},

	EffectiveDate: date(2024, 10, 1), // IMAER 6.0.0 published 2024-10-01 alongside the 2024 release (§2)
	Supersedes:    "2023",

	ImaerLine:            "6.0.x", // IMAER 6.0.0 aligns with AERIUS 2024 (§2)
	ImaerExactVersion:    "6.0.0",
	ImaerExactConfirmed:  false, // exact build binding not confirmed vs. live Connect (§6 convention)
	GMLProfile:           "GML 3.2.1 SF2 (GML-SF2)",
	BreakingSchemaChange: false,

	PointReleases: nil,

	SourceURLs: []string{
		"https://register.geostandaarden.nl/informatiemodel/imaer/", // IMAER 6.0.0 (2024-10-01)
	},
	CuratedOn: curatedOn,
	CuratedBy: curatedBy,
}

// release2025 — the AERIUS Calculator 2025 annual "actualisatie".
// Source: docs/regulatory/aerius-2025-release.md §1, §2, §8a.
var release2025 = AeriusRelease{
	Label:      "AERIUS Calculator 2025",
	VersionKey: "2025",
	Products:   []string{"Calculator", "Connect", "Monitor"}, // updated together (§1)

	EffectiveDate: date(2025, 10, 7), // mandatory under the Omgevingsregeling from 7 Oct 2025 (§1)
	Supersedes:    "2024",

	ImaerLine:            "6.0.x",                   // GML-SF2 / GML 3.2.1; sits on the 6.0.x line (§2)
	ImaerExactVersion:    "",                        // UNCONFIRMED for the Oct-2025 build (latest published = 6.0.1, 2025-04-24) (§2/§6)
	ImaerExactConfirmed:  false,                     // integration specialist confirms vs. live Connect before hard-coding GML ns/version
	GMLProfile:           "GML 3.2.1 SF2 (GML-SF2)", // unchanged at this release (§2)
	BreakingSchemaChange: false,                     // no evidence of a major IMAER bump; GML gen unlikely to break (§2)

	PointReleases: []PointRelease{
		// 2025.2 (2026-02-10) — UX/accessibility + background technical; results NOT affected (§1/§6).
		{Key: "2025.2", Date: date(2026, 2, 10), ResultsAffected: false},
	},

	SourceURLs: sources2025,
	CuratedOn:  curatedOn,
	CuratedBy:  curatedBy,
}

// delta2024to2025 — the curated 2024 -> 2025 delta.
// Source: docs/regulatory/aerius-2025-release.md §3, §4, §5, §8b.
var delta2024to2025 = AeriusReleaseDelta{
	FromVersion: "2024",
	ToVersion:   "2025",

	ChangeCategories: []ChangeCategory{
		CategoryEmissionFactors,
		CategorySourceCharacterisation,
		CategoryDispersionModel,
		CategoryBackgroundMaps,
		CategoryNatureHabitatData,
		CategoryIMAERModel,
	},
	// Short narrative per category — the §3 bullets, NOT factor values.
	CategoryNotes: map[ChangeCategory]string{
		CategoryEmissionFactors:        "Emissiefactoren herzien voor wegverkeer (NOx/NO2/NH3), zeescheepvaart, mobiele werktuigen en stalsystemen.",
		CategorySourceCharacterisation: "Mobiele werktuigen worden nu gekarakteriseerd op vermogensklasse i.p.v. sector (structurele invoerwijziging, geen losse getalswijziging).",
		CategoryDispersionModel:        "Verticale spreiding nu beschikbaar voor punt- en lijnbronnen (voorheen alleen vlakbronnen); onderliggend OPS-verspreidingsmodel geactualiseerd.",
		CategoryBackgroundMaps:         "Achtergronddepositiekaarten herbouwd op recentste emissiecijfers (basisjaar 2023) en metingen; kaartresolutie gestandaardiseerd (16-ha cross-year, 1-ha 2023 apart).",
		CategoryNatureHabitatData:      "Natuur-/habitatdata geactualiseerd in samenhang met de calculator; kan veranderen op welke receptoren/habitats een project neerslaat (KDW-waarden worden apart bestuurd).",
		CategoryIMAERModel:             "IMAER informatiemodel geactualiseerd binnen de 6.0.x-lijn (GML-SF2); geen aanwijzing voor een brekende GML-profielwijziging bij deze release.",
	},

	// COARSE direction — TRIAGE ONLY (ADR-009). The honest answer is mixed/source-dependent (§4).
	ExpectedDirection: DirectionMixedSourceDependent,
	// Per-source signals from a secondary technical commentary (stikstofinfo.net) citing the RIVM
	// report; sound for triage, NOT authoritative. Confidence "indicative" throughout (§4/§8b).
	DirectionBySource: []SourceDirection{
		{SourceType: "road_traffic_nox", Direction: "decrease", Confidence: "indicative"},
		{SourceType: "road_traffic_nh3", Direction: "neutral", Confidence: "indicative"},
		{SourceType: "shipping_nox", Direction: "increase", Confidence: "indicative"},
		{SourceType: "mobile_equipment_small", Direction: "increase", Confidence: "indicative"},
		{SourceType: "stables", Direction: "minor", Confidence: "indicative"},
		{SourceType: "background", Direction: "decrease", Confidence: "indicative"},
	},

	IdenticalInputsMayDiffer: true, // confirmed — BMD Advies (§4). This is WHY keep-alive exists.
	ReevalTrigger:            "recompute_required",
	ReevalPredicate:          `computed_under_version < "2025" AND route_uses_connect`,
	AuthoritativeSource:      "rivm_connect_recompute", // direction never substitutes for the recompute (ADR-009)

	Summary: "AERIUS Calculator 2025 (verplicht vanaf 7 oktober 2025) herziet emissiefactoren " +
		"(wegverkeer, zeescheepvaart, mobiele werktuigen, stallen), karakteriseert mobiele werktuigen " +
		"nu op vermogensklasse, actualiseert het OPS-verspreidingsmodel (verticale spreiding nu ook voor " +
		"punt- en lijnbronnen), vernieuwt de achtergrond- en natuurdata, en blijft op de IMAER 6.0.x-lijn " +
		"(GML-SF2). Het effect op de depositie is bron-afhankelijk en wisselend: bij gelijke invoer kunnen " +
		"resultaten anders uitvallen, dus een herberekening via AERIUS Connect is vereist. De richting per " +
		"bron is uitsluitend prioriterings-/triagecontext — de status volgt alleen uit de herberekening t.o.v. de KDW.",

	Confidence: "high (identity/date/trigger); medium (imaer exact; direction granularity)",
	UncertaintyFlags: []string{
		"imaer_exact_version_unconfirmed",
		"direction_is_triage_only",
		"point_release_semantics_per_release",
		"nature_data_receptor_reassignment",
	},
	SourceURLs: sources2025,
	CuratedOn:  curatedOn,
	CuratedBy:  curatedBy,
}

// NewRegistry returns the curated registry: the 2024 and 2025 releases (in ascending release
// order) and the 2024->2025 delta. This is the global, single source of version-abstraction truth
// (same for every tenant — ADR-009/010).
func NewRegistry() *Registry {
	return &Registry{
		releases: map[string]AeriusRelease{
			release2024.VersionKey: release2024,
			release2025.VersionKey: release2025,
		},
		order: []string{release2024.VersionKey, release2025.VersionKey}, // oldest -> newest
		deltas: map[string]AeriusReleaseDelta{
			deltaKey(delta2024to2025.FromVersion, delta2024to2025.ToVersion): delta2024to2025,
		},
	}
}
