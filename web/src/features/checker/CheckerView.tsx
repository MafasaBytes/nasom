import { useId, useState } from "react";
import type {
  CheckResult,
  DefensibilityStatus,
  NitrogenInputs,
  PromoteRequest,
} from "../../types/api";
import { ApiError, type ApiClient } from "../../api/client";
import { formatKm, formatMetric } from "../../shared/format";
import { statusPresentation } from "../../shared/status";
import { StatusIcon } from "../../shared/StatusIcon";
import { DepositionMeter } from "./DepositionMeter";
import { verdictCopy } from "./verdict";
import "./CheckerView.css";

interface CheckerViewProps {
  api: ApiClient;
  /** Called after a successful promote with the new assetId — App jumps to the monitor. */
  onPromoted: (assetId: string) => void;
}

// Build-phase intensity options (DESIGN §4.1). Value is the intensity multiplier the heuristic uses.
const INTENSITIES = [
  { value: 1, label: "Licht — beperkt materieel" },
  { value: 1.5, label: "Gemiddeld — regulier materieel & transport" },
  { value: 2.2, label: "Zwaar — heien, grondverzet, veel transport" },
] as const;

// Curated Natura 2000 areas (the seeded thresholds use these names; "— sterk overbelast" is a qualifier).
const AREAS = [
  "Veluwe",
  "Rijntakken",
  "Naardermeer",
  "Nieuwkoopse Plassen",
  "Maasduinen",
  "Arkemheen",
] as const;

interface FormState {
  name: string;
  area: string;
  distanceKm: number;
  homes: string;
  commercialM2: string;
  buildIntensity: number;
  internSalderen: boolean;
}

const INITIAL: FormState = {
  name: "Stadshaven Fase 2",
  area: "Veluwe",
  distanceKm: 1.5,
  homes: "240",
  commercialM2: "6000",
  buildIntensity: 1.5,
  internSalderen: false,
};

const DEPOSITION_METRIC = "deposition_mol_ha_yr";

export function CheckerView({ api, onPromoted }: CheckerViewProps) {
  const [form, setForm] = useState<FormState>(INITIAL);
  const [result, setResult] = useState<CheckResult | null>(null);
  const [checking, setChecking] = useState(false);
  const [promoting, setPromoting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [nameError, setNameError] = useState<string | null>(null);

  const nameId = useId();
  const nameErrId = useId();

  function buildInputs(): NitrogenInputs {
    return {
      natura2000_area: form.area,
      distance_km: form.distanceKm,
      homes: Number(form.homes) || 0,
      commercial_m2: Number(form.commercialM2) || 0,
      build_intensity: form.buildIntensity,
      routes: form.internSalderen ? ["intern_salderen"] : [],
    };
  }

  async function onCheck(e: React.FormEvent) {
    e.preventDefault();
    if (form.name.trim() === "") {
      setNameError("Geef het project een naam.");
      return;
    }
    setNameError(null);
    setError(null);
    setChecking(true);
    try {
      const res = await api.check({ domain: "nitrogen", inputs: buildInputs() });
      setResult(res);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "De toets kon niet worden uitgevoerd.");
    } finally {
      setChecking(false);
    }
  }

  async function onPromote() {
    if (!result) return;
    setPromoting(true);
    setError(null);
    try {
      const body: PromoteRequest = {
        domain: "nitrogen",
        inputs: buildInputs(),
        name: form.name.trim(),
        // ADR-004: the customer/consultant is the author of record. The seeded dev tenant is
        // van den Berg's consultancy; in production this comes from the authenticated user.
        authoredBy: "Royal HaskoningDHV",
        result: result.result,
      };
      const res = await api.promote(body);
      onPromoted(res.assetId);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Toevoegen aan de portefeuille mislukte.");
    } finally {
      setPromoting(false);
    }
  }

  const status: DefensibilityStatus | undefined = result?.status;
  const deposition = result?.result.metrics?.[DEPOSITION_METRIC];

  return (
    <section>
      <div className="hv-sec-h">
        <h1 className="hv-sec-title">Nieuwe locatie toetsen</h1>
        <span className="hv-sec-hint">
          — snelle voortoets: kan hier ontwikkeld worden, en hoe defensibel is dat?
        </span>
      </div>

      <div className="hv-checker">
        {/* ---- Form ---- */}
        <form className="hv-formcard" onSubmit={onCheck} noValidate>
          <label htmlFor={nameId}>Projectnaam</label>
          <input
            id={nameId}
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            aria-invalid={nameError ? true : undefined}
            aria-describedby={nameError ? nameErrId : undefined}
            className={nameError ? "is-invalid" : undefined}
            placeholder="bijv. Stadshaven Fase 2"
          />
          {nameError && (
            <div id={nameErrId} className="hv-field-error">
              {nameError}
            </div>
          )}

          <label htmlFor="hv-area">Dichtstbijzijnde Natura 2000-gebied</label>
          <select
            id="hv-area"
            value={form.area}
            onChange={(e) => setForm({ ...form, area: e.target.value })}
          >
            {AREAS.map((a) => (
              <option key={a} value={a}>
                {a} — overbelast gebied
              </option>
            ))}
          </select>

          <label htmlFor="hv-dist">
            Afstand tot gebied: <span className="hv-range-val">{formatKm(form.distanceKm)}</span>
          </label>
          <input
            id="hv-dist"
            type="range"
            min={0.3}
            max={12}
            step={0.1}
            value={form.distanceKm}
            aria-valuetext={formatKm(form.distanceKm)}
            onChange={(e) => setForm({ ...form, distanceKm: Number(e.target.value) })}
          />

          <label htmlFor="hv-homes">Woningen</label>
          <input
            id="hv-homes"
            type="number"
            min={0}
            value={form.homes}
            onChange={(e) => setForm({ ...form, homes: e.target.value })}
          />

          <label htmlFor="hv-comm">Commercieel programma (m²)</label>
          <input
            id="hv-comm"
            type="number"
            min={0}
            value={form.commercialM2}
            onChange={(e) => setForm({ ...form, commercialM2: e.target.value })}
          />

          <label htmlFor="hv-build">Bouwfase-intensiteit</label>
          <select
            id="hv-build"
            value={form.buildIntensity}
            onChange={(e) => setForm({ ...form, buildIntensity: Number(e.target.value) })}
          >
            {INTENSITIES.map((i) => (
              <option key={i.value} value={i.value}>
                {i.label}
              </option>
            ))}
          </select>

          <label className="hv-check-row">
            <input
              type="checkbox"
              checked={form.internSalderen}
              onChange={(e) => setForm({ ...form, internSalderen: e.target.checked })}
            />
            Leunt op intern salderen
          </label>

          <button type="submit" className="hv-btn" disabled={checking} aria-busy={checking}>
            {checking ? "Bezig met toetsen…" : "Toets de locatie"}
          </button>
        </form>

        {/* ---- Result ---- */}
        <div className="hv-resultcard">
          {error && (
            <div className="hv-inline-error" role="alert">
              <span>{error}</span>
            </div>
          )}

          {!result && !error && (
            <div className="hv-placeholder">
              Vul de gegevens in en klik <b>“Toets de locatie”</b>.<br />
              Je krijgt direct een indicatieve stikstofdepositie en een oordeel over de
              vergunbaarheid.
            </div>
          )}

          {result && status && (
            <>
              <Verdict status={status} />

              {deposition !== undefined && (
                <p className="hv-result-line">
                  Indicatieve depositie op <b>{form.area}</b>:{" "}
                  <b style={{ color: statusPresentation(status).tokens.fg }}>
                    {formatMetric(deposition, 3)} mol/ha/jr
                  </b>
                </p>
              )}

              {deposition !== undefined && (
                <DepositionMeter value={deposition} status={status} />
              )}

              {result.mitigations.length > 0 && (
                <div className="hv-mitigations">
                  <div className="hv-overline">Mitigatie-opties</div>
                  {result.mitigations.map((m) => (
                    <div key={m} className="hv-mit">
                      {m}
                    </div>
                  ))}
                  {status === "attention" && (
                    <div className="hv-mit-note">
                      Let op: intern salderen is beperkt door recente jurisprudentie
                      (ECLI:NL:RVS:2024:4923).
                    </div>
                  )}
                </div>
              )}

              <button
                type="button"
                className="hv-btn hv-btn-ghost"
                onClick={onPromote}
                disabled={promoting}
                aria-busy={promoting}
              >
                {promoting ? "Bezig met toevoegen…" : "+ Voeg toe aan portefeuille & monitor"}
              </button>

              <p className="hv-footnote">
                Indicatieve berekening — niet de wettelijke AERIUS-uitvoer. Een besluitbepalende
                berekening wordt onderbouwd met de officiële AERIUS Connect-uitvoer en is opgesteld
                onder verantwoordelijkheid van de opsteller.
              </p>
            </>
          )}
        </div>
      </div>
    </section>
  );
}

function Verdict({ status }: { status: DefensibilityStatus }) {
  const pres = statusPresentation(status);
  const copy = verdictCopy(status);
  return (
    <div className="hv-verdict" style={{ background: pres.tokens.bg }}>
      <span className="hv-verdict-ico" style={{ background: pres.tokens.solid }}>
        <StatusIcon
          shape={pres.shape}
          glyph={pres.glyph}
          color="#ffffff"
          glyphColor="#ffffff"
          size={28}
          filled={false}
        />
      </span>
      <span>
        <span className="hv-verdict-t" style={{ color: pres.tokens.fg }}>
          {copy.heading}
        </span>
        <span className="hv-verdict-s">{copy.sub}</span>
      </span>
    </div>
  );
}
