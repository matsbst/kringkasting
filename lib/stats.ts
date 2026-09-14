/**
 * In-memory operational counters for the admin dashboard. Reset on
 * restart by design — the dashboard labels them "siden omstart".
 * No per-user data is ever recorded.
 */

import { captureException } from "./errors.ts";

export const startedAt = new Date();

/**
 * Upstream health watch: alert (once, throttled) when NRK requests fail
 * in a sustained run, so a real outage or block gets surfaced to Bugsink
 * — without spamming on the transient 503/timeout that normal operation
 * shrugs off. Only network/timeout (0), 5xx, and 429/403 count as
 * failures; any other response (200, 301, 404) proves NRK is reachable
 * and clears the run — the alert is for sustained *unreachability*.
 */
const HEALTH_FAILURE_THRESHOLD = 8;
const HEALTH_ALERT_THROTTLE_MS = 30 * 60 * 1000;
let consecutiveFailures = 0;
let lastHealthAlertAt = 0;

function isHealthFailure(status: number): boolean {
  return status === 0 || status >= 500 || status === 429 || status === 403;
}

function recordUpstreamHealth(status: number) {
  if (!isHealthFailure(status)) {
    consecutiveFailures = 0;
    return;
  }
  consecutiveFailures++;
  if (consecutiveFailures >= HEALTH_FAILURE_THRESHOLD && Date.now() - lastHealthAlertAt > HEALTH_ALERT_THROTTLE_MS) {
    lastHealthAlertAt = Date.now();
    captureException(
      new Error(
        `NRK API appears unhealthy: ${consecutiveFailures} consecutive upstream failures (latest status ${status})`,
      ),
      { tags: { kind: "upstream-health", status: String(status) } },
    );
  }
}

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
  recordUpstreamHealth(status);
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

export const forTestingOnly = {
  isHealthFailure,
  feedStatus: recordUpstreamHealth,
  consecutiveFailures: () => consecutiveFailures,
  reset: () => {
    consecutiveFailures = 0;
    lastHealthAlertAt = 0;
  },
};
