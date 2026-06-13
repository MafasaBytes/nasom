# Houvast subagents

Tailored Claude Code subagents for building Houvast. They load automatically when this repo is opened
in Claude Code. Delegate scoped work to them; each one already knows our architecture and ADRs, so it
enforces decisions instead of rediscovering them. Every agent is told to read `CLAUDE.md` and
`docs/DECISIONS.md` first.

## Build agents (can write code)
- **go-backend-engineer** — core/app/adapters/api/worker in Go; enforces hexagonal boundaries.
- **aerius-integration-specialist** — AERIUS Connect client, IMAER GML, version layer, resilience
  (owns ADR-001/002/003 in code).
- **frontend-engineer** — TS/React monitor + checker; consumes `web/src/types/api.ts`.
- **test-engineer** — Go table-driven tests; ImpactEvaluator + tenant-isolation focus.
- **ui-ux-specialist** — interaction/visual design, the exposure/alert UX, design system; pairs with
  frontend-engineer.

## Review / specialist agents (read-only, except the analyst which curates data)
- **architecture-adr-guardian** — reviews for layer-boundary / AGPL / liability / tenancy violations.
- **regulatory-caselaw-analyst** — curates RvS rulings + AERIUS version deltas into machine-actionable
  scope for the ImpactEvaluator (the core IP). Can write the mapping data/config.
- **security-tenant-reviewer** — multi-tenant isolation, secrets, EU residency, API-key safety.

## Suggested flow
1. `regulatory-caselaw-analyst` defines the change/scope data the evaluator needs.
2. `aerius-integration-specialist` + `go-backend-engineer` implement the slice (ROADMAP M1).
3. `test-engineer` proves it (ImpactEvaluator + isolation).
4. `ui-ux-specialist` + `frontend-engineer` build the surfaces.
5. `architecture-adr-guardian` and `security-tenant-reviewer` review before merge.

Model assignments (sonnet/opus) are defaults — tune per the `model:` field in each file. Tools are
scoped intentionally: reviewers are read-only.
