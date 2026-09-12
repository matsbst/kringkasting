import { parse, toSeconds } from "iso8601-duration";
import { nrkRadio } from "./nrk/nrk.ts";
import { storage } from "./storage.ts";
import { canRetry, clearRetry, deferRetry } from "./retry.ts";

export type Chapter = { title: string | undefined; startTime: number | undefined };
type Result = { status: number; chapters: Chapter[] | null };
type Cached = Result & { expiresAt: number };
const TTL = 7 * 24 * 60 * 60_000;
const inFlight = new Map<string, Promise<Result>>();

export function getChapters(seriesId: string, episodeId: string): Promise<Result> {
  const key = `chapters:${seriesId}:${episodeId}`;
  const cached = storage.readState<Cached>(key);
  if (cached && cached.expiresAt > Date.now()) return Promise.resolve(cached);
  if (!canRetry(key)) return Promise.resolve(cached ?? { status: 503, chapters: null });
  let pending = inFlight.get(key);
  if (!pending) {
    pending = fetchChapters(key, seriesId, episodeId, cached).finally(() => inFlight.delete(key));
    inFlight.set(key, pending);
  }
  return pending;
}

async function fetchChapters(key: string, seriesId: string, episodeId: string, cached: Cached | null): Promise<Result> {
  try {
    const { episode, status } = await nrkRadio.getEpisodeResult(seriesId, episodeId);
    if (status !== 200 && status !== 404) throw new Error("Chapter lookup unavailable");
    const chapters = episode?.indexPoints?.map((point) => ({
      title: point.title,
      startTime: point.startPoint ? toSeconds(parse(point.startPoint)) : undefined,
    })) ?? null;
    const result = { status, chapters };
    storage.writeState(key, { ...result, expiresAt: Date.now() + TTL });
    clearRetry(key);
    return result;
  } catch {
    deferRetry(key);
    return cached ?? { status: 503, chapters: null };
  }
}
