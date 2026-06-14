import type { CSSProperties } from "react";
import { formatEur, formatMetric } from "../../shared/format";
import { isAtRisk, statusPresentation } from "../../shared/status";
import { StatusPill } from "../../shared/StatusPill";
import type { ProjectView } from "./projectView";
import "./ProjectCard.css";

interface ProjectCardProps {
  project: ProjectView;
  /** One-time emotional-beat pulse on the cards a just-landed event hit. */
  flash: boolean;
  onOpen: () => void;
  /** Ref so the drawer can return focus here on close. */
  registerRef: (el: HTMLButtonElement | null) => void;
}

// One card per monitored asset (DESIGN §2.3). The whole card is a button → opens the drawer.
// Status reads via a 5px stripe (decorative) + the pill (label+icon+colour) + euro relabel.
export function ProjectCard({ project, flash, onOpen, registerRef }: ProjectCardProps) {
  const pres = statusPresentation(project.status);
  const atRisk = isAtRisk(project.status);

  const style = {
    "--card-stripe": pres.tokens.solid,
    "--card-flash-bg": pres.tokens.bg,
  } as CSSProperties;

  const naturaLine = [project.natura2000, project.distanceKm !== undefined ? `${formatMetric(project.distanceKm, 1)} km` : undefined]
    .filter(Boolean)
    .join(" · ");

  return (
    <button
      type="button"
      ref={registerRef}
      className={`hv-card${atRisk ? " is-risk" : ""}${flash ? " is-flash" : ""}`}
      style={style}
      onClick={onOpen}
      aria-label={`${project.name} — status ${pres.label}. Open wat er veranderde.`}
    >
      <span className="hv-card-stripe" aria-hidden="true" />
      <span className="hv-card-head">
        <span className="hv-card-title">{project.name}</span>
        {project.indicative && (
          <span className="hv-card-indic" title="Indicatieve voortoets — nog te onderbouwen met officiële AERIUS-berekening">
            indicatief
          </span>
        )}
      </span>
      {project.location && <span className="hv-card-loc">{project.location}</span>}

      <span className="hv-card-rows">
        {naturaLine && (
          <span className="hv-card-row">
            <span className="hv-card-row-l">Natura 2000</span>
            <span>{naturaLine}</span>
          </span>
        )}
        <span className="hv-card-row">
          <span className="hv-card-row-l">Berekende depositie</span>
          <span className="hv-tnum">
            {project.deposition !== undefined ? (
              <>
                <b>{formatMetric(project.deposition)}</b> mol/ha/jr
              </>
            ) : (
              "—"
            )}
          </span>
        </span>
        {project.ruleVersionLabel && (
          <span className="hv-card-row">
            <span className="hv-card-row-l">Berekend in</span>
            <span>{project.ruleVersionLabel}</span>
          </span>
        )}
      </span>

      <span className="hv-card-foot">
        <StatusPill status={project.status} />
        <span
          className="hv-card-euro hv-tnum"
          style={atRisk ? { color: "var(--hv-color-euro-risk)" } : undefined}
        >
          {formatEur(project.capitalEur)}
          {atRisk ? " at risk" : ""}
        </span>
      </span>
    </button>
  );
}
