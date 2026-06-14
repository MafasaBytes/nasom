import type { TenantId } from "../types/api";

// The dev authn stub: every request carries X-Tenant-ID (ADR-006). Real JWT/OIDC is deferred.
// The API seeds these two tenants; we default to van den Berg (has intern_salderen + extern_salderen
// assets, so the demo flip is visible on its portfolio).
export const SEEDED_TENANTS: ReadonlyArray<{ id: TenantId; label: string }> = [
  { id: "tenant-vandenberg", label: "Van den Berg Vastgoed" },
  { id: "tenant-meridiaan", label: "Meridiaan Ontwikkeling" },
];

export const DEFAULT_TENANT: TenantId = "tenant-vandenberg";

const STORAGE_KEY = "hv.tenant";

export function loadTenant(): TenantId {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored && SEEDED_TENANTS.some((t) => t.id === stored)) {
      return stored;
    }
  } catch {
    // localStorage unavailable (private mode etc.) — fall through to default.
  }
  return DEFAULT_TENANT;
}

export function saveTenant(tenant: TenantId): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, tenant);
  } catch {
    // best-effort persistence only.
  }
}
