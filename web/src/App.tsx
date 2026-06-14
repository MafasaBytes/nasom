import { useMemo, useRef, useState } from "react";
import type { TenantId } from "./types/api";
import { createApiClient } from "./api/client";
import { loadTenant, saveTenant, SEEDED_TENANTS } from "./api/tenant";
import { MonitorView } from "./features/monitor/MonitorView";
import { CheckerView } from "./features/checker/CheckerView";
import "./App.css";

type View = "portfolio" | "checker";

// Dev tools (the ingest simulator) are gated to dev builds (DESIGN §3.5): they must not ship in the
// holder's production view. import.meta.env.DEV is true under `vite dev`, false in `vite build`.
const DEV_TOOLS = import.meta.env.DEV;

export function App() {
  const [tenant, setTenant] = useState<TenantId>(() => loadTenant());
  const [view, setView] = useState<View>("portfolio");
  const [promotedAssetId, setPromotedAssetId] = useState<string | null>(null);

  // Rebuild the client when the tenant changes; the new identity flows into every request header.
  const api = useMemo(() => createApiClient(tenant), [tenant]);

  const portfolioTabRef = useRef<HTMLButtonElement>(null);
  const checkerTabRef = useRef<HTMLButtonElement>(null);

  function onTenantChange(next: TenantId) {
    saveTenant(next);
    setTenant(next);
    setPromotedAssetId(null);
  }

  function onPromoted(assetId: string) {
    setPromotedAssetId(assetId);
    setView("portfolio");
  }

  // Tablist arrow-key navigation (DESIGN §5.1).
  function onTabKey(e: React.KeyboardEvent) {
    if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
      e.preventDefault();
      const next = view === "portfolio" ? "checker" : "portfolio";
      setView(next);
      (next === "portfolio" ? portfolioTabRef : checkerTabRef).current?.focus();
    }
  }

  return (
    <>
      <header className="hv-header">
        <div className="hv-logo">
          <span className="hv-logo-mark" aria-hidden="true">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
              <path
                d="M12 2 3 6v6c0 5 3.8 8.5 9 10 5.2-1.5 9-5 9-10V6l-9-4z"
                fill="#fff"
              />
              <path
                d="M9 12l2 2 4-4"
                stroke="#127f88"
                strokeWidth="2.2"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
          </span>
          Houvast <small>stikstofvergunningen onder controle</small>
        </div>

        <div className="hv-tabs" role="tablist" aria-label="Weergave" onKeyDown={onTabKey}>
          <button
            ref={portfolioTabRef}
            role="tab"
            type="button"
            aria-selected={view === "portfolio"}
            tabIndex={view === "portfolio" ? 0 : -1}
            className={`hv-tab${view === "portfolio" ? " is-active" : ""}`}
            onClick={() => setView("portfolio")}
          >
            Portefeuille
          </button>
          <button
            ref={checkerTabRef}
            role="tab"
            type="button"
            aria-selected={view === "checker"}
            tabIndex={view === "checker" ? 0 : -1}
            className={`hv-tab${view === "checker" ? " is-active" : ""}`}
            onClick={() => setView("checker")}
          >
            Nieuwe locatie toetsen
          </button>
        </div>

        <label className="hv-tenant">
          <span className="hv-tenant-lbl">Klant</span>
          <select
            value={tenant}
            onChange={(e) => onTenantChange(e.target.value)}
            aria-label="Klant (tenant) kiezen"
          >
            {SEEDED_TENANTS.map((t) => (
              <option key={t.id} value={t.id}>
                {t.label}
              </option>
            ))}
          </select>
        </label>
      </header>

      <main className="hv-wrap">
        {view === "portfolio" ? (
          <MonitorView
            api={api}
            devTools={DEV_TOOLS}
            promotedAssetId={promotedAssetId}
            onConsumePromoted={() => setPromotedAssetId(null)}
            goToChecker={() => setView("checker")}
          />
        ) : (
          <CheckerView api={api} onPromoted={onPromoted} />
        )}
      </main>

      <footer className="hv-footer">
        Houvast (werktitel) · berekeningen in de toets zijn <b>indicatief</b>, niet de wettelijke
        AERIUS-uitvoer · monitoring levert findings en aanbevelingen, geen garanties.
      </footer>
    </>
  );
}
