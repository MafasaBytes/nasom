import "./AlertBanner.css";

export interface BannerContent {
  /** "relief" = green confirmation (promote); "exposure" = a change-event landed. */
  tone: "exposure" | "relief";
  title: string;
  sub: string;
  /** Optional CTA → opens the most-exposed affected card's drawer (DESIGN §3.2). */
  action?: { label: string; onClick: () => void };
}

interface AlertBannerProps {
  content: BannerContent;
  onDismiss: () => void;
}

// Polite live region (DESIGN §3.1/§5.1): role="status", NOT assertive — the banner reframes
// ("vandaag gesignaleerd … niet maanden later"), it does not alarm. Relief, not panic.
export function AlertBanner({ content, onDismiss }: AlertBannerProps) {
  const isExposure = content.tone === "exposure";
  return (
    <div className={`hv-alert${isExposure ? " is-exposure" : " is-relief"}`} role="status">
      <span className="hv-alert-ico" aria-hidden="true">
        {isExposure ? "!" : "✓"}
      </span>
      <div className="hv-alert-body">
        <b className="hv-alert-title">{content.title}</b>
        <div className="hv-alert-sub">{content.sub}</div>
      </div>
      {content.action && (
        <button type="button" className="hv-alert-action" onClick={content.action.onClick}>
          {content.action.label}
        </button>
      )}
      <button type="button" className="hv-alert-x" onClick={onDismiss} aria-label="Melding sluiten">
        ✕
      </button>
    </div>
  );
}
