// nl-NL formatting helpers. The DESIGN spec mandates Intl, never hardcoded euro strings.

const eurCompact = new Intl.NumberFormat("nl-NL", {
  style: "currency",
  currency: "EUR",
  notation: "compact",
  maximumFractionDigits: 0,
});

/** € 366 mln. / € 91 mln. — KPI + per-project scale. */
export function formatEur(eur: number): string {
  return eurCompact.format(eur);
}

/** Format a deposition metric (mol/ha/jr) with a fixed number of fraction digits, nl-NL. */
export function formatMetric(value: number, fractionDigits = 2): string {
  return new Intl.NumberFormat("nl-NL", {
    minimumFractionDigits: fractionDigits,
    maximumFractionDigits: fractionDigits,
  }).format(value);
}

/** 1,5 km — slider value label / distance. */
export function formatKm(km: number): string {
  return `${new Intl.NumberFormat("nl-NL", { maximumFractionDigits: 1 }).format(km)} km`;
}

const dateFmt = new Intl.DateTimeFormat("nl-NL", {
  day: "numeric",
  month: "short",
  year: "numeric",
});

/** ISO-8601 → "7 okt. 2025". Returns the raw string if it cannot be parsed. */
export function formatDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return dateFmt.format(d);
}
