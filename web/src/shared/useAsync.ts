import { useCallback, useEffect, useRef, useState } from "react";

export interface AsyncState<T> {
  data: T | undefined;
  loading: boolean;
  error: string | undefined;
  /** Re-run the fetch (e.g. after an ingest, or a "retry"). */
  reload: () => void;
}

/**
 * Minimal typed data-fetching hook — no external state library (keeps deps lean per the brief).
 * Runs `fn` on mount and whenever `deps` change; aborts the in-flight request on change/unmount.
 * `fn` must accept the AbortSignal and pass it to the API client.
 */
export function useAsync<T>(
  fn: (signal: AbortSignal) => Promise<T>,
  deps: readonly unknown[],
): AsyncState<T> {
  const [data, setData] = useState<T | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | undefined>(undefined);
  const [tick, setTick] = useState(0);

  // Keep the latest fn without making it a dependency (callers often pass inline closures).
  const fnRef = useRef(fn);
  fnRef.current = fn;

  const reload = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setLoading(true);
    setError(undefined);

    fnRef.current(controller.signal).then(
      (result) => {
        if (!active) return;
        setData(result);
        setLoading(false);
      },
      (err: unknown) => {
        if (!active) return;
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof Error ? err.message : "Onbekende fout.");
        setLoading(false);
      },
    );

    return () => {
      active = false;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  return { data, loading, error, reload };
}
