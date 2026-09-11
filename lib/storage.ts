export type Episode = {
  id: string;
  title: string;
  subtitle: string | null;
  url: string;
  shareLink: string;
  date: Date;
  durationInSeconds: number;
};

export type Series = {
  id: string;
  title: string;
  subtitle: string | null;
  link: string;
  imageUrl: string;
  lastFetchedAt: Date;
  episodes: Episode[];
};

/**
 * When NRSS_KV_PATH is set (e.g. /app/data/cache.sqlite3 on Cloudron),
 * the KV database is stored there. Otherwise Deno picks a default
 * location, which is fine for local development.
 */
const kv = await Deno.openKv(Deno.env.get("NRSS_KV_PATH") ?? undefined);

type Identifiable = { id: string };
type Collection = "series";

function read<T extends Identifiable>(collection: Collection) {
  return async function (series: Identifiable) {
    const key = [collection, series.id];
    const read = await kv.get(key);
    return read.value as T | null;
  };
}

function write<T extends Identifiable>(collection: Collection) {
  return async function (entity: T) {
    const key = [collection, entity.id];
    const stored = await kv.set(key, entity);
    return stored.ok;
  };
}

const readSeries = read<Series>("series");
const writeSeries = write<Series>("series");

export const storage = {
  readSeries,
  writeSeries,
};
