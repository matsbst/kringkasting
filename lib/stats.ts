/**
 * In-memory operational counters for the admin dashboard. Reset on
 * restart by design — the dashboard labels them "siden omstart".
 * No per-user data is ever recorded.
 */

export const startedAt = new Date();

type UpstreamCategory = "katalog" | "episodeliste" | "manifest" | "filstørrelse" | "søk" | "annet";

const upstream: Record<UpstreamCategory, { requests: number; errors: number }> = {
  katalog: { requests: 0, errors: 0 },
  episodeliste: { requests: 0, errors: 0 },
  manifest: { requests: 0, errors: 0 },
  filstørrelse: { requests: 0, errors: 0 },
  søk: { requests: 0, errors: 0 },
  annet: { requests: 0, errors: 0 },
};

function categorize(url: string, method: string): UpstreamCategory {
  if (method === "HEAD") return "filstørrelse";
  if (url.includes("/playback/manifest/")) return "manifest";
  if (url.includes("/radio/search/search")) return "søk";
  if (url.includes("/episodes")) return "episodeliste";
  if (url.includes("/radio/catalog/") || url.includes("/radio/search/categories/")) return "katalog";
  return "annet";
}

export function recordUpstreamRequest(url: string, method: string, status: number) {
  const bucket = upstream[categorize(url, method)];
  bucket.requests++;
  if (status === 0 || status >= 500) {
    bucket.errors++;
  }
}

const served = {
  feeds200: 0,
  feeds304: 0,
  searches: 0,
  rateLimited: 0,
};

export function recordFeedServed(notModified: boolean) {
  if (notModified) {
    served.feeds304++;
  } else {
    served.feeds200++;
  }
}

export function recordSearchServed() {
  served.searches++;
}

export function recordRateLimited() {
  served.rateLimited++;
}

let lastGc: { at: Date; deleted: number } | null = null;

export function recordGcRun(deleted: number) {
  lastGc = { at: new Date(), deleted };
}

export function getStats() {
  return {
    startedAt,
    upstream: structuredClone(upstream),
    upstreamTotal: Object.values(upstream).reduce((sum, bucket) => sum + bucket.requests, 0),
    upstreamErrors: Object.values(upstream).reduce((sum, bucket) => sum + bucket.errors, 0),
    served: { ...served },
    lastGc,
  };
}
