/**
 * Tiny typed JSON GET helper, replacing the previous dependency on
 * https://deno.land/x/kall with a local implementation.
 *
 * Never throws: network errors and timeouts are reported as status 0
 * with a null body, so callers can treat every failure uniformly.
 */

import { canRetry, clearRetry, deferRetry, parseRetryAfter } from "./retry.ts";
import { recordUpstreamRequest } from "./stats.ts";

const REQUEST_TIMEOUT_MS = 15_000;

/**
 * Global cap on concurrent upstream requests, across ALL operations.
 * Per-operation pools (series fetch, backlog) bound their own fan-out,
 * but many simultaneous cold requests would multiply those pools; this
 * semaphore bounds the total pressure on NRK no matter what happens
 * above it.
 */
const MAX_CONCURRENT_UPSTREAM = 12;
/** beyond this, requests fail fast instead of queueing unboundedly */
const MAX_QUEUED_UPSTREAM = 64;
let inFlightUpstream = 0;
const upstreamWaiters: (() => void)[] = [];

class UpstreamSaturatedError extends Error {}

async function acquireUpstreamSlot(): Promise<void> {
  if (inFlightUpstream < MAX_CONCURRENT_UPSTREAM) {
    inFlightUpstream++;
    return;
  }
  if (upstreamWaiters.length >= MAX_QUEUED_UPSTREAM) {
    throw new UpstreamSaturatedError("upstream queue saturated");
  }
  await new Promise<void>((resolve) => upstreamWaiters.push(resolve));
}

function releaseUpstreamSlot(): void {
  const next = upstreamWaiters.shift();
  if (next) {
    // hand the slot directly to the next waiter
    next();
  } else {
    inFlightUpstream--;
  }
}

export type GetResult<T> = {
  status: number;
  body: T | null;
};

/** HEAD request returning the Content-Length, for enclosure byte sizes */
export async function head(url: string): Promise<{ status: number; contentLength: number | null }> {
  if (!canRetry(`HEAD:${url}`) || !canRetry(`origin:${new URL(url).origin}`)) {
    return { status: 503, contentLength: null };
  }
  try {
    await acquireUpstreamSlot();
  } catch {
    return { status: 0, contentLength: null };
  }
  try {
    // A previous in-flight request may have set Retry-After while we queued.
    if (!canRetry(`HEAD:${url}`) || !canRetry(`origin:${new URL(url).origin}`)) {
      return { status: 503, contentLength: null };
    }
    const response = await fetch(url, {
      method: "HEAD",
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    });
    await response.body?.cancel();
    const raw = response.headers.get("content-length");
    const contentLength = raw ? Number.parseInt(raw, 10) : null;
    recordUpstreamRequest(url, "HEAD", response.status);
    recordOutcome(url, "HEAD", response);
    return {
      status: response.status,
      contentLength: Number.isFinite(contentLength as number) ? contentLength : null,
    };
  } catch (error) {
    console.error(`HEAD ${url} failed: ${error}`);
    recordUpstreamRequest(url, "HEAD", 0);
    deferRetry(`HEAD:${url}`);
    return { status: 0, contentLength: null };
  } finally {
    releaseUpstreamSlot();
  }
}

export async function get<T>(url: string): Promise<GetResult<T>> {
  if (!canRetry(`GET:${url}`) || !canRetry(`origin:${new URL(url).origin}`)) return { status: 503, body: null };
  try {
    await acquireUpstreamSlot();
  } catch {
    return { status: 0, body: null };
  }
  try {
    if (!canRetry(`GET:${url}`) || !canRetry(`origin:${new URL(url).origin}`)) return { status: 503, body: null };
    const response = await fetch(url, {
      headers: { accept: "application/json" },
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    });

    recordUpstreamRequest(url, "GET", response.status);
    recordOutcome(url, "GET", response);
    if (!response.ok) {
      // consume the body so the connection can be released
      await response.body?.cancel();
      return { status: response.status, body: null };
    }

    const body = (await response.json()) as T;
    return { status: response.status, body };
  } catch (error) {
    console.error(`GET ${url} failed: ${error}`);
    recordUpstreamRequest(url, "GET", 0);
    deferRetry(`GET:${url}`);
    return { status: 0, body: null };
  } finally {
    releaseUpstreamSlot();
  }
}

export const forTestingOnly = { MAX_CONCURRENT_UPSTREAM };

function recordOutcome(url: string, method: string, response: Response) {
  const key = `${method}:${url}`;
  if (response.status === 429 || response.status >= 500) {
    const deadline = parseRetryAfter(response.headers.get("retry-after"));
    deferRetry(key, deadline);
    if (deadline > Date.now()) deferRetry(`origin:${new URL(url).origin}`, deadline);
  } else {
    clearRetry(key);
  }
}
