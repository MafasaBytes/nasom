package httpapi

// This file defines the JSON DTOs the HTTP API exposes and the mapping between core entities and
// those DTOs. The DTOs are the wire contract — their JSON tags MUST match web/src/types/api.ts
// EXACTLY (camelCase: tenantId, assetId, capitalAtRiskEur, newStatus, estimatedExposureEur, ...).
//
// The mapping lives HERE, at the adapter boundary, so the core stays clean: no JSON tags, no
// camelCase, no contract leakage cross into internal/core (docs/ARCHITECTURE.md). core uses Go-native
// types (int64 EUR, time.Time); the DTOs use the frontend's shapes (number EUR, ISO-8601 strings).
//
// LIABILITY POSTURE (ADR-004): nothing in these DTOs promises "guaranteed"/"compliant" — the
// explanation/recommendation/headline strings are pass-through of the domain's findings, which the
// evaluator and curated data keep as findings and recommendations, never guarantees.

import (
	"time"

	"github.com/houvast/houvast/internal/app"
	"github.com/houvast/houvast/internal/core"
)

// ---- Value DTOs ------------------------------------------------------------

type deltaDTO struct {
	Metric string  `json:"metric"`
	Old    float64 `json:"old"`
	New    float64 `json:"new"`
	Unit   string  `json:"unit"`
}

type assessmentResultDTO struct {
	Headline  string             `json:"headline"`
	Metrics   map[string]float64 `json:"metrics"`
	EngineRef string             `json:"engineRef"`
}

// ---- Entity DTOs -----------------------------------------------------------

type assetDTO struct {
	ID               string            `json:"id"`
	TenantID         string            `json:"tenantId"`
	Domain           string            `json:"domain"`
	Name             string            `json:"name"`
	Metadata         map[string]string `json:"metadata"`
	CapitalAtRiskEur int64             `json:"capitalAtRiskEur"`
	CreatedAt        string            `json:"createdAt"`
}

type assessmentDTO struct {
	ID               string              `json:"id"`
	AssetID          string              `json:"assetId"`
	TenantID         string              `json:"tenantId"`
	Domain           string              `json:"domain"`
	AuthoredBy       string              `json:"authoredBy"`
	RuleVersionLabel string              `json:"ruleVersionLabel"`
	Result           assessmentResultDTO `json:"result"`
	Status           string              `json:"status"`
	CreatedAt        string              `json:"createdAt"`
}

type findingDTO struct {
	AssessmentID         string    `json:"assessmentId"`
	ChangeEventID        string    `json:"changeEventId"`
	PreviousStatus       string    `json:"previousStatus"`
	NewStatus            string    `json:"newStatus"`
	Delta                *deltaDTO `json:"delta,omitempty"`
	Explanation          string    `json:"explanation"`
	Recommendation       string    `json:"recommendation"`
	EstimatedExposureEur int64     `json:"estimatedExposureEur"`
	EvaluatedAt          string    `json:"evaluatedAt"`
}

type exposureSnapshotDTO struct {
	TenantID           string `json:"tenantId"`
	TotalAssets        int    `json:"totalAssets"`
	ExposedAssets      int    `json:"exposedAssets"`
	AttentionAssets    int    `json:"attentionAssets"`
	CapitalPipelineEur int64  `json:"capitalPipelineEur"`
	CapitalAtRiskEur   int64  `json:"capitalAtRiskEur"`
	GeneratedAt        string `json:"generatedAt"`
}

// portfolioProjectDTO is the per-asset dashboard read model: an asset joined with its latest
// assessment and latest finding. The monitor frontend consumes Asset/Assessment/Finding (see
// web/src/features/README.md); this groups them per project. LatestAssessment/LatestFinding are
// pointers so they serialize to null when an asset has none yet.
type portfolioProjectDTO struct {
	Asset            assetDTO       `json:"asset"`
	LatestAssessment *assessmentDTO `json:"latestAssessment"`
	LatestFinding    *findingDTO    `json:"latestFinding"`
}

// ---- check / promote DTOs --------------------------------------------------

// checkRequestDTO mirrors web/src/types/api.ts CheckRequest. Inputs are domain-specific and passed
// opaquely to the domain (nitrogen: area, distance, homes, m2, intensity, routes). The tenant comes
// from the X-Tenant-ID header (ADR-006), NOT the body, so it cannot be spoofed per request.
type checkRequestDTO struct {
	Domain string `json:"domain"`
	Inputs any    `json:"inputs"`
}

// checkResultDTO mirrors CheckResult plus the indicative verdict + mitigations. ADR-001: indicative,
// never authoritative. ADR-004: verdict/mitigations are options, never guarantees.
type checkResultDTO struct {
	Result      assessmentResultDTO `json:"result"`
	Status      string              `json:"status"`
	Verdict     string              `json:"verdict"`
	Mitigations []string            `json:"mitigations"`
}

// promoteRequestDTO is the body of POST /api/promote. AuthoredBy is the customer/consultant of record
// (ADR-004) — it comes from the request, NEVER defaulted to Houvast. Result is the indicative result
// the user saw at check time (it is re-stamped PROVISIONAL on promotion, ADR-001).
type promoteRequestDTO struct {
	Domain     string              `json:"domain"`
	Inputs     any                 `json:"inputs"`
	Name       string              `json:"name"`
	AuthoredBy string              `json:"authoredBy"`
	Result     assessmentResultDTO `json:"result"`
}

// promoteResponseDTO is the result of a successful promotion: the created asset id.
type promoteResponseDTO struct {
	AssetID string `json:"assetId"`
}

// ingestResponseDTO is the dev/admin ingest result: the findings produced and the per-tenant
// exposure snapshots from one worker cycle, so the UI can reproduce the demo flip.
type ingestResponseDTO struct {
	Events    []changeEventDTO      `json:"events"`
	Findings  []findingDTO          `json:"findings"`
	Snapshots []exposureSnapshotDTO `json:"snapshots"`
	Errors    []string              `json:"errors"`
}

type changeEventDTO struct {
	ID          string `json:"id"`
	Domain      string `json:"domain"`
	Kind        string `json:"kind"`
	Ref         string `json:"ref"`
	Summary     string `json:"summary"`
	EffectiveAt string `json:"effectiveAt"`
}

// ---- core -> DTO mapping ---------------------------------------------------

func toAssetDTO(a core.Asset) assetDTO {
	md := a.Metadata
	if md == nil {
		md = map[string]string{}
	}
	return assetDTO{
		ID:               string(a.ID),
		TenantID:         string(a.TenantID),
		Domain:           string(a.Domain),
		Name:             a.Name,
		Metadata:         md,
		CapitalAtRiskEur: a.CapitalAtRiskEUR,
		CreatedAt:        a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func toAssessmentResultDTO(r core.AssessmentResult) assessmentResultDTO {
	m := r.Metrics
	if m == nil {
		m = map[string]float64{}
	}
	return assessmentResultDTO{Headline: r.Headline, Metrics: m, EngineRef: r.EngineRef}
}

func toAssessmentDTO(a core.Assessment) assessmentDTO {
	return assessmentDTO{
		ID:               string(a.ID),
		AssetID:          string(a.AssetID),
		TenantID:         string(a.TenantID),
		Domain:           string(a.Domain),
		AuthoredBy:       a.AuthoredBy,
		RuleVersionLabel: a.RuleVersion.Label,
		Result:           toAssessmentResultDTO(a.Result),
		Status:           string(a.Status),
		CreatedAt:        a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func toFindingDTO(f core.Finding) findingDTO {
	var delta *deltaDTO
	if f.Delta != nil {
		delta = &deltaDTO{Metric: f.Delta.Metric, Old: f.Delta.Old, New: f.Delta.New, Unit: f.Delta.Unit}
	}
	return findingDTO{
		AssessmentID:         string(f.AssessmentID),
		ChangeEventID:        string(f.ChangeEventID),
		PreviousStatus:       string(f.PreviousStatus),
		NewStatus:            string(f.NewStatus),
		Delta:                delta,
		Explanation:          f.Explanation,
		Recommendation:       f.Recommendation,
		EstimatedExposureEur: f.EstimatedExposureEUR,
		EvaluatedAt:          f.EvaluatedAt.UTC().Format(time.RFC3339),
	}
}

func toExposureSnapshotDTO(s core.ExposureSnapshot) exposureSnapshotDTO {
	return exposureSnapshotDTO{
		TenantID:           string(s.TenantID),
		TotalAssets:        s.TotalAssets,
		ExposedAssets:      s.ExposedAssets,
		AttentionAssets:    s.AttentionAssets,
		CapitalPipelineEur: s.CapitalPipelineEUR,
		CapitalAtRiskEur:   s.CapitalAtRiskEUR,
		GeneratedAt:        s.GeneratedAt.UTC().Format(time.RFC3339),
	}
}

func toPortfolioProjectDTO(p app.PortfolioProject) portfolioProjectDTO {
	dto := portfolioProjectDTO{Asset: toAssetDTO(p.Asset)}
	if p.LatestAssessment != nil {
		a := toAssessmentDTO(*p.LatestAssessment)
		dto.LatestAssessment = &a
	}
	if p.LatestFinding != nil {
		f := toFindingDTO(*p.LatestFinding)
		dto.LatestFinding = &f
	}
	return dto
}

func toCheckResultDTO(r app.CheckResult) checkResultDTO {
	mits := r.Mitigations
	if mits == nil {
		mits = []string{}
	}
	return checkResultDTO{
		Result:      toAssessmentResultDTO(r.Result),
		Status:      string(r.Status),
		Verdict:     string(r.Verdict),
		Mitigations: mits,
	}
}

func toChangeEventDTO(e core.ChangeEvent) changeEventDTO {
	return changeEventDTO{
		ID:          string(e.ID),
		Domain:      string(e.Domain),
		Kind:        string(e.Kind),
		Ref:         e.Ref,
		Summary:     e.Summary,
		EffectiveAt: e.EffectiveAt.UTC().Format(time.RFC3339),
	}
}
