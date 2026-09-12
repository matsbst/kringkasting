import { storage } from "./storage.ts";

type RetryState = { attempts: number; nextRetryAt: number };
const PREFIX = "retry:";

export function retryAt(key: string): number {
  return storage.readState<RetryState>(PREFIX + key)?.nextRetryAt ?? 0;
}

export function canRetry(key: string): boolean {
  return retryAt(key) <= Date.now();
}

/** Five minutes, ten minutes, ... up to a day; upstream deadlines take precedence. */
export function deferRetry(key: string, notBefore = 0): void {
  const previous = storage.readState<RetryState>(PREFIX + key);
  const attempts = Math.min((previous?.attempts ?? 0) + 1, 10);
  const delay = Math.min(24 * 60 * 60_000, 5 * 60_000 * 2 ** (attempts - 1));
  storage.writeState(PREFIX + key, { attempts, nextRetryAt: Math.max(Date.now() + delay, notBefore) });
}

export function clearRetry(key: string): void {
  storage.deleteState(PREFIX + key);
}

export function parseRetryAfter(value: string | null, now = Date.now()): number {
  if (!value) return 0;
  if (/^\d+$/.test(value.trim())) {
    const deadline = now + Number(value) * 1000;
    return Number.isFinite(deadline) ? deadline : 0;
  }
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? Math.max(now, parsed) : 0;
}
