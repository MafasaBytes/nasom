import { formatMetric } from "../../shared/format";
import { verdictCopy } from "./verdict";
import type { DefensibilityStatus } from "../../types/api";
import "./DepositionMeter.css";

interface DepositionMeterProps {
  value: number; // mol/ha/jr
  status: DefensibilityStatus;
}

// Horizontal gradient bar with a needle (DESIGN §4.3). Because a gradient is colour-only, it is
// SUPPORTED not RELIED upon: the verdict pill + numeric value carry the status; the meter is a
// secondary spatial cue. The container exposes role=img + aria-label so SR users get the meaning.
const SCALE_MAX = 0.15; // the bar maps 0..0,15+ across its width.

export function DepositionMeter({ value, status }: DepositionMeterProps) {
  const pct = Math.max(0, Math.min(98, (value / SCALE_MAX) * 100));
  const label = `Indicatieve depositie ${formatMetric(value, 3)} mol per hectare per jaar, oordeel: ${verdictCopy(status).heading}`;

  return (
    <div className="hv-meter-wrap" role="img" aria-label={label}>
      <div className="hv-meter">
        <span className="hv-meter-needle" style={{ left: `${pct}%` }} aria-hidden="true" />
      </div>
      <div className="hv-meter-scale" aria-hidden="true">
        <span>0 — vrij</span>
        <span>0,05</span>
        <span>0,15+ — zwaar</span>
      </div>
    </div>
  );
}
