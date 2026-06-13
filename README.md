# Houvast

**Keep Dutch nitrogen (stikstof) permit work defensible — and know the instant it isn't.**

Houvast continuously re-tests a portfolio of AERIUS nitrogen assessments against every new AERIUS
version and Raad van State ruling, and flags which permits have become legally vulnerable — with the
euro exposure and the recommended action. Plus a fast location pre-check that promotes a project
straight into the monitor.

> New here? Read **`CLAUDE.md`** first — it's the project brief.

## Status

**Architecture + interfaces only (ROADMAP M0).** No running app yet. The domain-generic core
(`internal/core` — entities + ports) and the API contract types (`web/src/types`) are defined and
compile-ready; the nitrogen adapters and app services exist as stubs that `panic("not implemented")`.
`cmd/` and `internal/adapters/` are empty skeletons to be filled per `docs/ROADMAP.md` (M1 → M4).

## Stack

- **Backend:** Go, ports-and-adapters (hexagonal). Domain-generic core + per-vertical adapters.
- **Frontend:** TypeScript + React.
- **Data:** Postgres. Cloud-agnostic deployment.

## Layout (target)

```
houvast/
├── CLAUDE.md                  # project brain (read first)
├── README.md                  # you are here
├── go.mod
├── cmd/
│   ├── api/                   # HTTP API entrypoint
│   └── worker/                # change-event ingestion / re-evaluation worker
├── internal/
│   ├── core/                  # domain-generic entities + PORT interfaces (depends on nothing)
│   ├── app/                   # application services (MonitorService, CheckService)
│   ├── domains/
│   │   └── nitrogen/          # nitrogen adapters implementing the core ports (AERIUS, RvS)
│   └── adapters/
│       ├── persistence/       # Postgres repositories
│       ├── httpapi/           # HTTP handlers
│       └── notify/            # alert delivery
└── web/                       # TypeScript/React frontend
    └── src/
        ├── types/             # API contract types (mirror of core DTOs)
        └── features/          # monitor, checker
```

> **Internal docs are not tracked in this repo.** The `docs/` folder (architecture, ADRs, product
> spec, roadmap, market research, concept demo) is gitignored and lives on the maintainer's machine,
> read by Claude Code locally. `CLAUDE.md` and the agents in `.claude/agents/` reference `docs/`
> paths — those resolve in a local working copy. If you've cloned this repo and lack `docs/`, ask the
> maintainer for the context pack.

## Two pre-build validation gates

Before significant code, two real-world facts must be confirmed — both are potential kill-criteria:
1. **Liability posture** — that "tool, not adviser; customer is author" holds under Dutch law.
2. **AERIUS Connect commercial terms** — that bulk commercial API use is tolerated, and the
   AERIUS-divestment governance question is understood.

## The non-negotiables (full reasoning in `CLAUDE.md` §4 + ADRs)

- Arms-length AERIUS Connect; never embed the engine (AGPL).
- Persist engine results immediately (Connect expires ~3 days, no SLA).
- Customer is always the assessment author; we emit findings, never guarantees.
- Multi-tenant isolation is sacred; cross-tenant learning is aggregate-only (the moat).
