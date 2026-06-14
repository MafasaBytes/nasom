// Typed fetch wrapper for the Houvast HTTP API (internal/adapters/httpapi).
// Every request carries the X-Tenant-ID header (ADR-006 dev authn stub). No `any` at the boundary:
// responses are typed against web/src/types/api.ts.

import type {
  AssessmentId,
  CheckRequest,
  CheckResult,
  ExposureSnapshot,
  Finding,
  IngestResult,
  PortfolioProject,
  PromoteRequest,
  PromoteResponse,
  TenantId,
} from "../types/api";

const TENANT_HEADER = "X-Tenant-ID";

// The uniform error envelope the API returns ({ "error": "..." }).
interface ApiErrorBody {
  error?: string;
}

/** A failed API call. Carries the HTTP status and the server's safe, caller-facing message. */
export class ApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

async function request<T>(
  path: string,
  tenant: TenantId,
  init?: { method?: string; body?: unknown; signal?: AbortSignal },
): Promise<T> {
  const headers: Record<string, string> = { [TENANT_HEADER]: tenant };
  if (init?.body !== undefined) {
    headers["Content-Type"] = "application/json";
  }

  let res: Response;
  try {
    res = await fetch(path, {
      method: init?.method ?? "GET",
      headers,
      body: init?.body !== undefined ? JSON.stringify(init.body) : undefined,
      signal: init?.signal,
    });
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === "AbortError") {
      throw cause;
    }
    throw new ApiError(0, "Kan de server niet bereiken.");
  }

  if (!res.ok) {
    let message = "Er ging iets mis.";
    try {
      const body = (await res.json()) as ApiErrorBody;
      if (body.error) message = body.error;
    } catch {
      // non-JSON error body — keep the generic message.
    }
    throw new ApiError(res.status, message);
  }

  // 204 / empty body guard (none of our endpoints currently do this, but be safe).
  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

export interface ApiClient {
  portfolio(signal?: AbortSignal): Promise<PortfolioProject[]>;
  exposure(signal?: AbortSignal): Promise<ExposureSnapshot>;
  findings(id: AssessmentId, signal?: AbortSignal): Promise<Finding[]>;
  check(req: CheckRequest, signal?: AbortSignal): Promise<CheckResult>;
  promote(req: PromoteRequest, signal?: AbortSignal): Promise<PromoteResponse>;
  /** DEV/ADMIN: drive one keep-alive worker cycle and reproduce the demo flip. */
  ingest(signal?: AbortSignal): Promise<IngestResult>;
}

/** Builds a tenant-bound client. All calls send the given tenant in X-Tenant-ID (ADR-006). */
export function createApiClient(tenant: TenantId): ApiClient {
  return {
    portfolio: (signal) =>
      request<PortfolioProject[]>("/api/portfolio", tenant, { signal }),
    exposure: (signal) =>
      request<ExposureSnapshot>("/api/portfolio/exposure", tenant, { signal }),
    findings: (id, signal) =>
      request<Finding[]>(
        `/api/assessments/${encodeURIComponent(id)}/findings`,
        tenant,
        { signal },
      ),
    check: (req, signal) =>
      request<CheckResult>("/api/check", tenant, {
        method: "POST",
        body: req,
        signal,
      }),
    promote: (req, signal) =>
      request<PromoteResponse>("/api/promote", tenant, {
        method: "POST",
        body: req,
        signal,
      }),
    ingest: (signal) =>
      request<IngestResult>("/api/ingest", tenant, { method: "POST", signal }),
  };
}
