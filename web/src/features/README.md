# Frontend features

TypeScript/React. Two feature areas mirror the two product surfaces (see `docs/PRODUCT_SPEC.md`).
The clickable concept demo at `docs/Houvast_MVP_demo.html` is the visual north star for both.

- `monitor/` — Surface A: portfolio dashboard (exposure KPIs, project cards with status pills,
  the "what changed" drawer). Consumes `ExposureSnapshot`, `Asset`, `Assessment`, `Finding`.
- `checker/` — Surface B: location pre-check form → verdict + deposition meter + mitigation options
  → "promote into portfolio". Consumes `CheckRequest`/`CheckResult`.

All API types live in `web/src/types/api.ts` (kept in sync with Go core DTOs).
Status: not built yet — see `docs/ROADMAP.md` M1/M3.
