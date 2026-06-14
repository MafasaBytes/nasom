import type { CSSProperties } from "react";
import type { ExposureSnapshot } from "../../types/api";
import { formatEur } from "../../shared/format";
import { statusPresentation } from "../../shared/status";
import { StatusIcon } from "../../shared/StatusIcon";
import "./ExposureKpis.css";

interface ExposureKpisProps {
  snapshot: ExposureSnapshot | undefined;
  loading: boolean;
}

// The hero band (DESIGN §2.2): in <2s the holder reads "how many of mine are exposed, how much money
// is at risk." KPIs 3 & 4 change colour AND meaning when exposure exists; colour never carries it alone
// (an icon chip + a 3px top stripe + the sub-copy all agree).
export function ExposureKpis({ snapshot, loading }: ExposureKpisProps) {
  if (loading || !snapshot) {
    return <KpiSkeleton />;
  }

  const exposed = snapshot.exposedAssets;
  const attention = snapshot.attentionAssets;
  const atRiskCapital = snapshot.capitalAtRiskEur;

  // KPI 3 dominant figure: exposed if any, else attention, else calm 0 (DESIGN §2.2).
  const exposedActive = exposed > 0;
  const attentionOnly = exposed === 0 && attention > 0;
  const k3Active = exposedActive || attentionOnly;
  const k3Status = exposedActive ? "exposed" : "attention";
  const k3Value = exposedActive ? exposed : attention;
  const k3Pres = statusPresentation(k3Status);

  let k3Sub: string;
  if (exposedActive && attention > 0) k3Sub = `+${attention} ter herziening`;
  else if (exposedActive) k3Sub = "actie vereist";
  else if (attentionOnly) k3Sub = "ter herziening";
  else k3Sub = "alles defensibel";

  const riskActive = atRiskCapital > 0;
  const riskStatus = statusPresentation("exposed");

  return (
    <section className="hv-kpis" aria-label="Blootstellingsoverzicht">
      <Kpi label="Projecten gemonitord" sub="gemonitord">
        <span className="hv-kpi-val hv-tnum">{snapshot.totalAssets}</span>
      </Kpi>

      <Kpi label="Kapitaal in pijplijn" sub="ontwikkelwaarde">
        <span className="hv-kpi-val hv-tnum">{formatEur(snapshot.capitalPipelineEur)}</span>
      </Kpi>

      <Kpi
        label="Vergunningen blootgesteld"
        sub={k3Sub}
        active={k3Active}
        accent={k3Active ? k3Pres.tokens.solid : undefined}
      >
        <span className="hv-kpi-row">
          {k3Active && (
            <StatusIcon
              shape={k3Pres.shape}
              glyph={k3Pres.glyph}
              color={k3Pres.tokens.solid}
              size={22}
            />
          )}
          <span
            className="hv-kpi-val hv-tnum"
            style={k3Active ? { color: k3Pres.tokens.fg } : undefined}
          >
            {k3Value}
          </span>
        </span>
      </Kpi>

      <Kpi
        label='Kapitaal "at risk"'
        sub="door wijziging geraakt"
        active={riskActive}
        accent={riskActive ? riskStatus.tokens.solid : undefined}
      >
        <span className="hv-kpi-row">
          {riskActive && (
            <StatusIcon
              shape={riskStatus.shape}
              glyph={riskStatus.glyph}
              color={riskStatus.tokens.solid}
              size={22}
            />
          )}
          <span
            className="hv-kpi-val hv-tnum"
            style={riskActive ? { color: "var(--hv-color-euro-risk)" } : undefined}
          >
            {formatEur(atRiskCapital)}
          </span>
        </span>
      </Kpi>
    </section>
  );
}

interface KpiProps {
  label: string;
  sub: string;
  active?: boolean;
  accent?: string;
  children: React.ReactNode;
}

function Kpi({ label, sub, active = false, accent, children }: KpiProps) {
  const style = accent ? ({ "--kpi-accent": accent } as CSSProperties) : undefined;
  return (
    <div className={`hv-kpi${active ? " is-active" : ""}`} style={style}>
      <div className="hv-kpi-lbl">{label}</div>
      {children}
      <div className="hv-kpi-sub">{sub}</div>
    </div>
  );
}

function KpiSkeleton() {
  return (
    <section className="hv-kpis" aria-busy="true" aria-label="Blootstellingsoverzicht laden">
      {[0, 1, 2, 3].map((i) => (
        <div className="hv-kpi" key={i}>
          <div className="hv-kpi-lbl">
            <span className="hv-skel hv-skel-sm" />
          </div>
          <div className="hv-skel hv-skel-lg" />
          <div className="hv-kpi-sub">
            <span className="hv-skel hv-skel-sm" />
          </div>
        </div>
      ))}
    </section>
  );
}
