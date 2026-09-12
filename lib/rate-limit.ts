import { recordRateLimited } from "./stats.ts";

/**
 * Small in-memory token buckets, keyed per client, for the two request
 * types that can trigger upstream work: searches and cold feed fetches.
 * Cached/known requests are never limited.
 */

type Bucket = {
  tokens: number;
  updatedAt: number;
};

const MAX_BUCKETS = 5_000;
const buckets = new Map<string, Bucket>();

function prune(now: number) {
  if (buckets.size < MAX_BUCKETS) {
    return;
  }
  for (const [key, bucket] of buckets) {
    // idle for 10+ minutes: fully refilled anyway
    if (now - bucket.updatedAt > 10 * 60 * 1000) {
      buckets.delete(key);
    }
  }
}

/**
 * Consume one token from the client's bucket; false = rate limited.
 * `capacity` is the burst allowance, `refillPerSecond` the sustained rate.
 */
export function allowRequest(
  key: string,
  capacity: number,
  refillPerSecond: number,
  now = Date.now(),
): boolean {
  prune(now);

  let bucket = buckets.get(key);
  if (!bucket) {
    bucket = { tokens: capacity, updatedAt: now };
    buckets.set(key, bucket);
  }

  bucket.tokens = Math.min(capacity, bucket.tokens + ((now - bucket.updatedAt) / 1000) * refillPerSecond);
  bucket.updatedAt = now;

  if (bucket.tokens < 1) {
    recordRateLimited();
    return false;
  }
  bucket.tokens -= 1;
  return true;
}

/**
 * Per-client limit plus a global backstop for the same scope. Client
 * identity comes from forwarding headers, which a direct-to-origin
 * attacker can forge — the global bucket bounds total damage regardless.
 */
export function allowScoped(
  scope: string,
  clientKey: string,
  clientCapacity: number,
  clientRefillPerSecond: number,
  globalCapacity: number,
  globalRefillPerSecond: number,
): boolean {
  if (!allowRequest(`global:${scope}`, globalCapacity, globalRefillPerSecond)) {
    return false;
  }
  return allowRequest(`${scope}:${clientKey}`, clientCapacity, clientRefillPerSecond);
}

/** best client identity available behind Cloudflare/nginx */
export function getClientKey(request: Request): string {
  return request.headers.get("cf-connecting-ip") ??
    request.headers.get("x-forwarded-for")?.split(",")[0].trim() ??
    "unknown";
}
