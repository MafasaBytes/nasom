package caselaw

import "time"

// This file embeds the curated nitrogen case-law data. EVERY value here traces to the
// regulatory-caselaw-analyst's cited note: docs/regulatory/intern-salderen-2024.md (curated
// 2026-06-13). Primary-source URLs are carried on each record (audit trail, ADR-004).
//
// Reminder of the hard boundaries this file honours:
//   - Curated, cited data only — no judgement logic (the route predicate + retroactivity decision
//     lives in the evaluator's pure judge(), never here).
//   - Sober remediation text (ADR-004): "waarschijnlijk vergunningplichtig", "laat ... opnieuw
//     toetsen", "mogelijk een overgangstermijn", "raadpleeg" — never "compliant"/"guaranteed".

// date is a small helper for readable literal dates (UTC).
func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// curatedOn / curatedBy are the shared provenance stamp for this curation batch.
var (
	curatedOn = date(2026, 6, 13)
	curatedBy = "regulatory-caselaw-analyst"
)

// sourcesInternSalderen2024 are the primary URLs backing the ECLI:NL:RVS:2024:4923 record.
// Source list: docs/regulatory/intern-salderen-2024.md header (all retrieved 2026-06-13).
var sourcesInternSalderen2024 = []string{
	"https://www.raadvanstate.nl/actueel/nieuws/december/rechtspraak-over-intern-salderen-wijzigt/",
	"https://uitspraken.rechtspraak.nl/details?id=ECLI:NL:RVS:2024:4923",
	"https://uitspraken.rechtspraak.nl/details?id=ECLI:NL:RVS:2024:4909",
	"https://www.rnmw.nl/ons-kantoor/publicaties/annotaties/ABRvS_18_december_2024_ECLI-NL-RVS-2024-4923/",
	"https://www.houthoff.com/nl-nl/actueel/news-update/wijzigingen-in-de-rechtspraak-over-intern-salderen",
	"https://www.stibbe.com/publications-and-insights/netherlands-further-locked-in-council-of-state-limits-internal-netting-of",
	"https://www.bij12.nl/onderwerp/stikstof/passende-beoordeling/intern-salderen/",
}

// rulingInternSalderen2024 — ABRvS 18 december 2024, intern salderen weer vergunningplichtig.
// The canonical anchor of the doctrinal shift; the companion 4909 (Amercentrale) is recorded for the
// audit trail but NOT separately emitted (note §6.1). Curated values: docs/regulatory/intern-salderen-2024.md §5.
var rulingInternSalderen2024 = Ruling{
	ECLI:          "ECLI:NL:RVS:2024:4923",
	CompanionECLI: "ECLI:NL:RVS:2024:4909",
	Court:         "Afdeling bestuursrechtspraak van de Raad van State",
	RulingDate:    date(2024, time.December, 18), // 18 december 2024 (note §5: effective_at 2024-12-18)

	// An assessment is hit if NitrogenInputs.Routes contains this route (note §5 predicate_route).
	PredicateRoute: "intern_salderen",

	// Retroactive: reaches assessments authored before the effective date. The 1-1-2020..1-1-2030
	// transition period is an enforcement moratorium, not a defence — surfaced in the recommendation,
	// never as a reason to keep status defensible (note §3 / §6.3).
	Retroactive: true,

	// Thin-event summary; reads cleanly mid-sentence after "...; " in the evaluator's explanation
	// (note §5). Matches the ChangeEvent.summary in the analyst's note.
	Summary: "ABRvS 18-12-2024: intern salderen mag niet meer in de voortoets; project weer vergunningplichtig.",

	// Sober, ADR-004-compliant remediation. VERBATIM the NL value the analyst embeds in
	// CaseLawScope.Recommendation (note §4 / §5).
	Recommendation: "Deze beoordeling steunt op intern salderen in de voortoets. Sinds ABRvS 18-12-2024 " +
		"(ECLI:NL:RVS:2024:4923) is intern salderen daarvoor niet meer toereikend en is het project " +
		"waarschijnlijk vergunningplichtig. Laat de natuurvergunningplicht opnieuw toetsen; voor activiteiten " +
		"die tussen 1-1-2020 en 1-1-2025 zijn gestart geldt mogelijk een overgangstermijn tot 1-1-2030. " +
		"Raadpleeg de vergunningverlener / uw adviseur.",

	SourceURLs: sourcesInternSalderen2024,
	Confidence: "high (core holding settled; transition-period mechanics carry nuance — note §6)",
	CuratedOn:  curatedOn,
	CuratedBy:  curatedBy,
}

// NewRegistry returns the curated case-law registry: the one curated nitrogen ruling
// (ECLI:NL:RVS:2024:4923, intern salderen weer vergunningplichtig). This is the global, single source
// of case-law-abstraction truth (same for every tenant — ADR-009/010).
func NewRegistry() *Registry {
	rulings := []Ruling{rulingInternSalderen2024} // ascending ruling-date order (one entry)
	byECLI := make(map[string]Ruling, len(rulings))
	for _, r := range rulings {
		byECLI[r.ECLI] = r
	}
	return &Registry{rulings: rulings, byECLI: byECLI}
}
