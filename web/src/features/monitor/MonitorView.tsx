import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ChangeEvent, ExposureSnapshot, PortfolioProject } from "../../types/api";
import { ApiError, type ApiClient } from "../../api/client";
import { formatEur } from "../../shared/format";
import { useAsync } from "../../shared/useAsync";
import { AlertBanner, type BannerContent } from "./AlertBanner";
import { ChangeDrawer } from "./ChangeDrawer";
import { ExposureKpis } from "./ExposureKpis";
import { ProjectCard } from "./ProjectCard";
import { resolveSource } from "./changeEvents";
import { toProjectView } from "./projectView";
import "./MonitorView.css";

interface MonitorViewProps {
  api: ApiClient;
  /** Dev-only: show the ingest simulator (the demo flip). Removed from the holder's prod view. */
  devTools: boolean;
  /** A just-promoted asset to flash + announce on arrival (Surface B on-ramp). */
  promotedAssetId: string | null;
  onConsumePromoted: () => void;
  goToChecker: () => void;
}

export function MonitorView({
  api,
  devTools,
  promotedAssetId,
  onConsumePromoted,
  goToChecker,
}: MonitorViewProps) {
  const portfolio = useAsync<PortfolioProject[]>((s) => api.portfolio(s), [api]);
  const exposure = useAsync<ExposureSnapshot>((s) => api.exposure(s), [api]);

  const [openAssetId, setOpenAssetId] = useState<string | null>(null);
  const [banner, setBanner] = useState<BannerContent | null>(null);
  const [flashIds, setFlashIds] = useState<ReadonlySet<string>>(new Set());
  const [ingesting, setIngesting] = useState(false);
  // ChangeEvents learned from ingest. See changeEvents.ts: the finding→event id-join is unreliable
  // (the live API serializes empty ids), so the drawer resolves by ref/content, not by a keyed map.
  const [events, setEvents] = useState<readonly ChangeEvent[]>([]);

  const cardRefs = useRef(new Map<string, HTMLButtonElement | null>());
  const flashTimer = useRef<number | undefined>(undefined);

  const projects = useMemo(
    () => (portfolio.data ?? []).map(toProjectView),
    [portfolio.data],
  );

  const openProject = projects.find((p) => p.assetId === openAssetId) ?? null;

  const reloadAll = useCallback(() => {
    portfolio.reload();
    exposure.reload();
  }, [portfolio, exposure]);

  // One-time flash guard (DESIGN §3.5): flash the given assets once, then settle to calm.
  const flashOnce = useCallback((ids: string[]) => {
    if (ids.length === 0) return;
    setFlashIds(new Set(ids));
    window.clearTimeout(flashTimer.current);
    flashTimer.current = window.setTimeout(() => setFlashIds(new Set()), 1200);
  }, []);

  useEffect(() => () => window.clearTimeout(flashTimer.current), []);

  // ---- The emotional beat: ingest → refetch → flip + reframe (DESIGN §3) -------------------
  const runIngest = useCallback(async () => {
    setIngesting(true);
    try {
      const result = await api.ingest();

      // Remember the change events so the drawer can show the ECLI/version ref + dates.
      // De-dupe by ref (ids are empty in the current contract) so re-ingests don't pile up.
      setEvents((prev) => {
        const byRef = new Map(prev.map((e) => [e.ref, e]));
        for (const ev of result.events) if (ev.ref !== "") byRef.set(ev.ref, ev);
        return Array.from(byRef.values());
      });

      // Refetch the portfolio so cards flip to their new status; then flash the affected ones.
      const fresh = await api.portfolio();
      portfolio.reload();
      exposure.reload();

      const affectedAssessmentIds = new Set(result.findings.map((f) => f.assessmentId));
      const affected = fresh.filter(
        (p) => p.latestAssessment && affectedAssessmentIds.has(p.latestAssessment.id),
      );
      const affectedAssetIds = affected.map((p) => p.asset.id);
      flashOnce(affectedAssetIds);

      if (affected.length > 0) {
        const totalExposure = result.findings.reduce(
          (sum, f) => sum + f.estimatedExposureEur,
          0,
        );
        // Reframe copy from the event the findings actually point to (not merely the first detected
        // event — the version path is gated, so the flip is driven by the case-law ruling).
        const source = resolveSource(result.findings[0], result.events);
        const ref = source.event?.ref ?? "een nieuwe wijziging";
        const isCaseLaw = source.kind === "case_law";
        const occasion = isCaseLaw ? "uitspraak" : "release";
        setBanner({
          tone: "exposure",
          title: isCaseLaw
            ? `${affected.length} ${plural(affected.length, "project")} leunen op een route die door een uitspraak is geraakt`
            : `${affected.length} ${plural(affected.length, "project")} geraakt door ${ref}`,
          sub: `${formatEur(totalExposure)} blootgesteld — vandaag gesignaleerd, op de dag van de ${occasion}, niet maanden later bij een bezwaar.`,
          action: {
            label: "Bekijk wat er veranderde",
            onClick: () => setOpenAssetId(affectedAssetIds[0] ?? null),
          },
        });
        window.scrollTo({ top: 0, behavior: "smooth" });
      } else {
        // Version path is gated (ADR-001/002) → events detected but no flip. Be honest about it.
        setBanner({
          tone: "relief",
          title: "Wijziging gedetecteerd — geen project geraakt",
          sub:
            result.errors.length > 0
              ? "Een herberekening kon nog niet worden uitgevoerd; de bestaande oordelen blijven ongewijzigd tot dat lukt."
              : "Er zijn geen assessments die door deze wijziging hun defensibiliteit verliezen.",
        });
      }
    } catch (err) {
      setBanner({
        tone: "exposure",
        title: "Ingest mislukt",
        sub: err instanceof ApiError ? err.message : "Onbekende fout bij het verwerken.",
      });
    } finally {
      setIngesting(false);
    }
  }, [api, portfolio, exposure, flashOnce]);

  // ---- Promote arrival (from Surface B) ----------------------------------------------------
  useEffect(() => {
    if (!promotedAssetId) return;
    // Refetch so the new card appears, flash it, and show the calm confirmation banner.
    (async () => {
      try {
        const fresh = await api.portfolio();
        portfolio.reload();
        exposure.reload();
        const arrived = fresh.find((p) => p.asset.id === promotedAssetId);
        flashOnce([promotedAssetId]);
        setBanner({
          tone: "relief",
          title: "Project toegevoegd aan de monitor",
          sub: `${arrived?.asset.name ?? "Het project"} wordt nu continu bewaakt. Je hoeft er niet meer aan te denken — Houvast wel.`,
        });
        window.scrollTo({ top: 0, behavior: "smooth" });
      } finally {
        onConsumePromoted();
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [promotedAssetId]);

  // ---- Render ------------------------------------------------------------------------------
  const loading = portfolio.loading || exposure.loading;
  const hasError = portfolio.error ?? exposure.error;
  const isEmpty = !loading && !hasError && projects.length === 0;

  return (
    <section>
      {banner && <AlertBanner content={banner} onDismiss={() => setBanner(null)} />}

      {devTools && (
        <div className="hv-dock">
          <div className="hv-dock-h">▶ Dev-aansturing — simuleer een externe gebeurtenis (niet zichtbaar in productie)</div>
          <button type="button" className="hv-dock-btn" onClick={runIngest} disabled={ingesting}>
            {ingesting ? "Bezig met verwerken…" : "⚡ Pas wijziging toe (ingest)"}
          </button>
        </div>
      )}

      {hasError && !isEmpty && (
        <div className="hv-portfolio-error" role="alert">
          <span>De portefeuille kon niet worden geladen.</span>
          <button type="button" className="hv-textbtn" onClick={reloadAll}>
            Opnieuw proberen
          </button>
        </div>
      )}

      {!isEmpty && <ExposureKpis snapshot={exposure.data} loading={exposure.loading} />}

      {isEmpty ? (
        <EmptyState onStart={goToChecker} />
      ) : (
        <>
          <div className="hv-sec-h">
            <h1 className="hv-sec-title">Portefeuille-monitor</h1>
            <span className="hv-sec-hint">
              — elke berekening wordt continu getoetst aan de actuele AERIUS-versie én nieuwe
              jurisprudentie
            </span>
          </div>

          <div className="hv-grid">
            {loading && projects.length === 0
              ? [0, 1, 2, 3].map((i) => <div key={i} className="hv-card-skel" aria-hidden="true" />)
              : projects.map((p) => (
                  <ProjectCard
                    key={p.assetId}
                    project={p}
                    flash={flashIds.has(p.assetId)}
                    onOpen={() => setOpenAssetId(p.assetId)}
                    registerRef={(el) => cardRefs.current.set(p.assetId, el)}
                  />
                ))}
          </div>
        </>
      )}

      {openProject && (
        <ChangeDrawer
          project={openProject}
          api={api}
          events={events}
          onClose={() => setOpenAssetId(null)}
        />
      )}
    </section>
  );
}

function EmptyState({ onStart }: { onStart: () => void }) {
  return (
    <div className="hv-empty">
      <svg width="40" height="40" viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <path
          d="M12 2 3 6v6c0 5 3.8 8.5 9 10 5.2-1.5 9-5 9-10V6l-9-4z"
          fill="var(--hv-color-teal)"
        />
        <path
          d="M9 12l2 2 4-4"
          stroke="#fff"
          strokeWidth="2.2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
      <h2 className="hv-empty-title">Nog geen projecten in de monitor.</h2>
      <p className="hv-empty-body">
        Toets een locatie en voeg die toe — daarna bewaakt Houvast de berekening automatisch.
      </p>
      <button type="button" className="hv-btn" onClick={onStart}>
        Nieuwe locatie toetsen
      </button>
    </div>
  );
}

function plural(n: number, word: string): string {
  return n === 1 ? word : `${word}en`;
}
