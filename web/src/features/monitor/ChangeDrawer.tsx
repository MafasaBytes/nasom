import { useId, useRef } from "react";
import type { ChangeEvent, Finding } from "../../types/api";
import { formatDate, formatEur, formatMetric } from "../../shared/format";
import { statusPresentation } from "../../shared/status";
import { StatusPill } from "../../shared/StatusPill";
import { useFocusTrap } from "../../shared/useFocusTrap";
import { useAsync } from "../../shared/useAsync";
import type { ApiClient } from "../../api/client";
import { resolveSource } from "./changeEvents";
import type { ProjectView } from "./projectView";
import "./ChangeDrawer.css";

interface ChangeDrawerProps {
  project: ProjectView;
  api: ApiClient;
  /** ChangeEvents learned from the last ingest (for the ref/kind/date chip). See resolveSource. */
  events: readonly ChangeEvent[];
  onClose: () => void;
}

// The "Wat veranderde er" drawer (DESIGN §2.4): the payoff surface. A Finding told as a story —
// what / by how much / what to do / on whose authority / when. role=dialog, focus-trapped, Esc closes.
export function ChangeDrawer({ project, api, events, onClose }: ChangeDrawerProps) {
  const panelRef = useRef<HTMLDivElement>(null);
  const titleId = useId();
  useFocusTrap(panelRef, true, onClose);

  const assessmentId = project.assessmentId;
  const findingsState = useAsync<Finding[]>(
    (signal) => (assessmentId ? api.findings(assessmentId, signal) : Promise.resolve([])),
    [assessmentId],
  );

  // newest finding first.
  const findings = (findingsState.data ?? [])
    .slice()
    .sort((a, b) => b.evaluatedAt.localeCompare(a.evaluatedAt));

  return (
    <>
      <div className="hv-scrim" onClick={onClose} aria-hidden="true" />
      <div
        ref={panelRef}
        className="hv-drawer"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
      >
        <header className="hv-drawer-head">
          <button type="button" className="hv-drawer-close" onClick={onClose} aria-label="Sluiten">
            ✕
          </button>
          <StatusPill status={project.status} />
          <h2 id={titleId} className="hv-drawer-title">
            {project.name}
          </h2>
          {project.location && <div className="hv-drawer-loc">{project.location}</div>}
          {project.indicative && (
            <div className="hv-drawer-indic">
              Indicatieve voortoets — nog te onderbouwen met officiële AERIUS-berekening.
            </div>
          )}
        </header>

        <div className="hv-drawer-body">
          <dl className="hv-kv">
            <KV label="Ontwikkelwaarde" value={formatEur(project.capitalEur)} />
            <KV
              label="Natura 2000"
              value={project.natura2000 ?? "—"}
              sub={project.distanceKm !== undefined ? `${formatMetric(project.distanceKm, 1)} km` : undefined}
            />
            <KV
              label="Depositie"
              value={
                project.deposition !== undefined
                  ? `${formatMetric(project.deposition)} mol/ha/jr`
                  : "—"
              }
            />
            <KV
              label="Berekend in"
              value={project.ruleVersionLabel ?? "—"}
              sub={formatDate(project.authoredAtIso)}
            />
          </dl>

          {findingsState.loading && <div className="hv-drawer-shimmer" aria-busy="true" />}

          {findingsState.error && (
            <div className="hv-inline-error" role="alert">
              <span>De wijzigingen konden niet worden geladen.</span>
              <button type="button" onClick={findingsState.reload} className="hv-textbtn">
                Opnieuw proberen
              </button>
            </div>
          )}

          {!findingsState.loading &&
            !findingsState.error &&
            findings.length === 0 &&
            project.status === "defensible" && <DefensibleBlock />}

          {findings.map((f, i) => (
            <FindingBlock key={`${f.evaluatedAt}-${i}`} finding={f} events={events} />
          ))}

          <Timeline project={project} findings={findings} events={events} />
        </div>
      </div>
    </>
  );
}

function KV({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="hv-kv-box">
      <dt className="hv-kv-l">{label}</dt>
      <dd className="hv-kv-v">
        {value}
        {sub && <span className="hv-kv-sub">{sub}</span>}
      </dd>
    </div>
  );
}

function DefensibleBlock() {
  return (
    <div className="hv-change hv-change-defensible">
      ✓ Deze berekening is getoetst aan de actuele AERIUS-versie en de meest recente jurisprudentie.
      Geen actie nodig. Je krijgt automatisch bericht zodra dat verandert.
    </div>
  );
}

function FindingBlock({ finding, events }: { finding: Finding; events: readonly ChangeEvent[] }) {
  const pres = statusPresentation(finding.newStatus);
  const { kind, event } = resolveSource(finding, events);
  const isCaseLaw = kind === "case_law";
  const sourceGlyph = isCaseLaw ? "⚖" : "⚙";
  const heading = isCaseLaw
    ? "Wat veranderde er — jurisprudentie"
    : "Wat veranderde er — AERIUS-actualisatie";

  return (
    <section
      className="hv-change"
      style={{
        background: pres.tokens.bg,
        borderColor: pres.tokens.line,
      }}
    >
      <h3 className="hv-change-title" style={{ color: pres.tokens.fg }}>
        <span aria-hidden="true">{sourceGlyph}</span> {heading}
      </h3>
      <p className="hv-change-text">{finding.explanation}</p>

      {finding.delta && (
        <p className="hv-delta hv-tnum">
          <span className="hv-delta-old">{formatMetric(finding.delta.old)}</span>
          <span className="hv-delta-arrow" aria-hidden="true">
            →
          </span>
          <span className="hv-delta-new" style={{ color: pres.tokens.fg }}>
            {formatMetric(finding.delta.new)} {finding.delta.unit}
          </span>
        </p>
      )}

      {event && (
        <p className="hv-ref-chip">
          <span className="hv-ref-mono">{event.ref}</span>
          <span className="hv-ref-date">{formatDate(event.effectiveAt)}</span>
        </p>
      )}

      <div className="hv-action">
        <div className="hv-action-t">Aanbevolen actie</div>
        <div>{finding.recommendation}</div>
        {finding.estimatedExposureEur > 0 && (
          <div className="hv-action-exp hv-tnum">
            Geschatte blootstelling: <b>{formatEur(finding.estimatedExposureEur)}</b>
          </div>
        )}
      </div>
    </section>
  );
}

function Timeline({
  project,
  findings,
  events,
}: {
  project: ProjectView;
  findings: Finding[];
  events: readonly ChangeEvent[];
}) {
  return (
    <div className="hv-timeline-wrap">
      <div className="hv-overline">Dossier-tijdlijn</div>
      <ol className="hv-timeline">
        <li>
          {formatDate(project.authoredAtIso)} — voortoets/berekening opgesteld
          {project.ruleVersionLabel ? ` (${project.ruleVersionLabel})` : ""}
        </li>
        {findings
          .slice()
          .sort((a, b) => a.evaluatedAt.localeCompare(b.evaluatedAt))
          .map((f, i) => {
            const { event } = resolveSource(f, events);
            const pres = statusPresentation(f.newStatus);
            return (
              <li key={`${f.evaluatedAt}-${i}`} style={{ color: pres.tokens.fg }}>
                {formatDate(f.evaluatedAt)} — {event ? event.summary : "automatisch hertoetst"}
              </li>
            );
          })}
        <li>
          Nu —{" "}
          {project.status === "defensible"
            ? "defensibel, continu bewaakt"
            : "actie aanbevolen"}
        </li>
      </ol>
    </div>
  );
}
