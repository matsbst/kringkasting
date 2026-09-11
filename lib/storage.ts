import { DatabaseSync } from "node:sqlite";

export type Episode = {
  id: string;
  title: string;
  subtitle: string | null;
  url: string;
  shareLink: string;
  date: Date;
  durationInSeconds: number;
  /** enclosure file size in bytes, when known */
  bytes?: number | null;
};

export type Series = {
  id: string;
  title: string;
  subtitle: string | null;
  link: string;
  imageUrl: string;
  lastFetchedAt: Date;
  /** true once the full episode archive has been crawled */
  backlogComplete?: boolean;
  /** NRK API href of the next backlog page to crawl, null = start over/done */
  backlogCursor?: string | null;
  episodes: Episode[];
};

const SCHEMA = `
CREATE TABLE IF NOT EXISTS series (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  subtitle TEXT,
  link TEXT NOT NULL,
  image_url TEXT NOT NULL,
  last_fetched_at INTEGER NOT NULL,
  backlog_complete INTEGER NOT NULL DEFAULT 0,
  backlog_cursor TEXT
);
CREATE TABLE IF NOT EXISTS episodes (
  series_id TEXT NOT NULL,
  id TEXT NOT NULL,
  title TEXT NOT NULL,
  subtitle TEXT,
  url TEXT NOT NULL,
  share_link TEXT NOT NULL,
  date INTEGER NOT NULL,
  duration_seconds INTEGER NOT NULL,
  bytes INTEGER,
  PRIMARY KEY (series_id, id)
);
CREATE INDEX IF NOT EXISTS episodes_series_date ON episodes(series_id, date DESC);
`;

/**
 * In-memory per-series data version, bumped on every content write.
 * Lets the feed cache know when its rendered XML is out of date without
 * touching the database. Resets on restart (so does the feed cache).
 */
const dataVersions = new Map<string, number>();

function bumpDataVersion(seriesId: string) {
  dataVersions.set(seriesId, (dataVersions.get(seriesId) ?? 0) + 1);
}

function getDataVersion(seriesId: string): number {
  return dataVersions.get(seriesId) ?? 0;
}

let db: DatabaseSync | null = null;

/**
 * Lazily opened so importing this module (e.g. during the vite build)
 * doesn't create database files. NRSS_DB_PATH configures the location
 * (/app/data/nrss.sqlite3 on Cloudron); ":memory:" works for tests.
 */
function getDb(): DatabaseSync {
  if (db) {
    return db;
  }
  const path = Deno.env.get("NRSS_DB_PATH") ?? "nrss.sqlite3";
  db = new DatabaseSync(path);
  db.exec("PRAGMA journal_mode = WAL;");
  db.exec("PRAGMA busy_timeout = 5000;");
  db.exec(SCHEMA);
  return db;
}

type SeriesRow = {
  id: string;
  title: string;
  subtitle: string | null;
  link: string;
  image_url: string;
  last_fetched_at: number;
  backlog_complete: number;
  backlog_cursor: string | null;
};

type EpisodeRow = {
  id: string;
  title: string;
  subtitle: string | null;
  url: string;
  share_link: string;
  date: number;
  duration_seconds: number;
  bytes: number | null;
};

function readSeries(options: { id: string }): Series | null {
  const database = getDb();
  const row = database
    .prepare("SELECT * FROM series WHERE id = ?")
    .get(options.id) as SeriesRow | undefined;
  if (!row) {
    return null;
  }

  const episodeRows = database
    .prepare("SELECT * FROM episodes WHERE series_id = ? ORDER BY date DESC")
    .all(options.id) as EpisodeRow[];

  return {
    id: row.id,
    title: row.title,
    subtitle: row.subtitle,
    link: row.link,
    imageUrl: row.image_url,
    lastFetchedAt: new Date(row.last_fetched_at),
    backlogComplete: row.backlog_complete === 1,
    backlogCursor: row.backlog_cursor,
    episodes: episodeRows.map((episode) => ({
      id: episode.id,
      title: episode.title,
      subtitle: episode.subtitle,
      url: episode.url,
      shareLink: episode.share_link,
      date: new Date(episode.date),
      durationInSeconds: episode.duration_seconds,
      bytes: episode.bytes,
    })),
  };
}

/**
 * Upsert the series metadata (refreshing lastFetchedAt) and add/refresh
 * its episodes. Existing episodes NOT in `series.episodes` are kept —
 * that is what lets the archive accumulate beyond NRK's page size.
 * Backlog state is not touched here.
 */
function writeSeries(series: Series): boolean {
  const database = getDb();
  const upsertSeries = database.prepare(`
    INSERT INTO series (id, title, subtitle, link, image_url, last_fetched_at)
    VALUES (?, ?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET
      title = excluded.title,
      subtitle = excluded.subtitle,
      link = excluded.link,
      image_url = excluded.image_url,
      last_fetched_at = excluded.last_fetched_at
  `);

  database.exec("BEGIN");
  try {
    upsertSeries.run(
      series.id,
      series.title,
      series.subtitle,
      series.link,
      series.imageUrl,
      series.lastFetchedAt.getTime(),
    );
    insertEpisodes(database, series.id, series.episodes);
    database.exec("COMMIT");
    bumpDataVersion(series.id);
    return true;
  } catch (error) {
    database.exec("ROLLBACK");
    console.error(`Failed to write series ${series.id}: ${error}`);
    return false;
  }
}

/** add episodes to an existing series (used by the backlog crawler) */
function addEpisodes(seriesId: string, episodes: Episode[]): boolean {
  const database = getDb();
  database.exec("BEGIN");
  try {
    insertEpisodes(database, seriesId, episodes);
    database.exec("COMMIT");
    bumpDataVersion(seriesId);
    return true;
  } catch (error) {
    database.exec("ROLLBACK");
    console.error(`Failed to add episodes to ${seriesId}: ${error}`);
    return false;
  }
}

function insertEpisodes(database: DatabaseSync, seriesId: string, episodes: Episode[]) {
  const upsert = database.prepare(`
    INSERT INTO episodes (series_id, id, title, subtitle, url, share_link, date, duration_seconds, bytes)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(series_id, id) DO UPDATE SET
      title = excluded.title,
      subtitle = excluded.subtitle,
      url = excluded.url,
      share_link = excluded.share_link,
      date = excluded.date,
      duration_seconds = excluded.duration_seconds,
      bytes = COALESCE(excluded.bytes, episodes.bytes)
  `);
  for (const episode of episodes) {
    upsert.run(
      seriesId,
      episode.id,
      episode.title,
      episode.subtitle,
      episode.url,
      episode.shareLink,
      episode.date.getTime(),
      episode.durationInSeconds,
      episode.bytes ?? null,
    );
  }
}

function readEpisodeIds(seriesId: string): Set<string> {
  const rows = getDb()
    .prepare("SELECT id FROM episodes WHERE series_id = ?")
    .all(seriesId) as { id: string }[];
  return new Set(rows.map((row) => row.id));
}

function setBacklogState(seriesId: string, cursor: string | null, complete: boolean): void {
  getDb()
    .prepare("UPDATE series SET backlog_cursor = ?, backlog_complete = ? WHERE id = ?")
    .run(cursor, complete ? 1 : 0, seriesId);
}

export const storage = {
  readSeries,
  writeSeries,
  addEpisodes,
  readEpisodeIds,
  setBacklogState,
  getDataVersion,
};
