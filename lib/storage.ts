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

/** which NRK catalog a series lives in — decides the endpoints used */
export type CatalogKind = "podcast" | "series";

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
  catalogKind?: CatalogKind | null;
  /** umbrella shows list episodes per season and always need full refreshes */
  isUmbrella?: boolean;
  /** when title/artwork was last fetched from NRK (refreshed weekly) */
  metadataRefreshedAt?: Date | null;
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
  backlog_cursor TEXT,
  catalog_kind TEXT,
  is_umbrella INTEGER NOT NULL DEFAULT 0,
  metadata_refreshed_at INTEGER
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
CREATE TABLE IF NOT EXISTS failed_episodes (
  series_id TEXT NOT NULL,
  id TEXT NOT NULL,
  attempts INTEGER NOT NULL,
  next_retry_at INTEGER NOT NULL,
  PRIMARY KEY (series_id, id)
);
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
 * doesn't create database files. KRINGKASTING_DB_PATH configures the location
 * (/app/data/kringkasting.sqlite3 on Cloudron); ":memory:" works for tests.
 */
function getDb(): DatabaseSync {
  if (db) {
    return db;
  }
  const path = Deno.env.get("KRINGKASTING_DB_PATH") ?? "kringkasting.sqlite3";
  db = new DatabaseSync(path);
  db.exec("PRAGMA journal_mode = WAL;");
  db.exec("PRAGMA busy_timeout = 5000;");
  db.exec(SCHEMA);
  // additive migrations for databases created before these columns existed
  const migrations = [
    "ALTER TABLE series ADD COLUMN catalog_kind TEXT",
    "ALTER TABLE series ADD COLUMN is_umbrella INTEGER NOT NULL DEFAULT 0",
    "ALTER TABLE series ADD COLUMN metadata_refreshed_at INTEGER",
  ];
  for (const migration of migrations) {
    try {
      db.exec(migration);
    } catch {
      // column already exists
    }
  }
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
  catalog_kind: string | null;
  is_umbrella: number;
  metadata_refreshed_at: number | null;
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

/** cheap existence check without loading episodes */
function hasSeries(seriesId: string): boolean {
  return getDb().prepare("SELECT 1 FROM series WHERE id = ?").get(seriesId) !== undefined;
}

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
    catalogKind: row.catalog_kind as CatalogKind | null,
    isUmbrella: row.is_umbrella === 1,
    metadataRefreshedAt: row.metadata_refreshed_at === null ? null : new Date(row.metadata_refreshed_at),
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
    INSERT INTO series (id, title, subtitle, link, image_url, last_fetched_at, catalog_kind, is_umbrella, metadata_refreshed_at)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(id) DO UPDATE SET
      title = excluded.title,
      subtitle = excluded.subtitle,
      link = excluded.link,
      image_url = excluded.image_url,
      last_fetched_at = excluded.last_fetched_at,
      catalog_kind = COALESCE(excluded.catalog_kind, series.catalog_kind),
      is_umbrella = excluded.is_umbrella,
      metadata_refreshed_at = excluded.metadata_refreshed_at
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
      series.catalogKind ?? null,
      series.isUmbrella ? 1 : 0,
      // writeSeries is the full-metadata path; stamp the metadata as fresh
      Date.now(),
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

/** renew the freshness timestamp without touching metadata (episodes-only refresh) */
function touchLastFetched(seriesId: string): void {
  getDb()
    .prepare("UPDATE series SET last_fetched_at = ? WHERE id = ?")
    .run(Date.now(), seriesId);
}

function setBacklogState(seriesId: string, cursor: string | null, complete: boolean): void {
  getDb()
    .prepare("UPDATE series SET backlog_cursor = ?, backlog_complete = ? WHERE id = ?")
    .run(cursor, complete ? 1 : 0, seriesId);
}

const DAY_MS = 24 * 60 * 60 * 1000;
const MAX_RETRY_BACKOFF_DAYS = 30;

/**
 * Episodes whose playback manifest failed (geo-blocked, expired,
 * removed) are retried with exponential backoff — 1 day, 2, 4, …,
 * capped at 30 — instead of on every refresh forever.
 */
function recordEpisodeFailure(seriesId: string, episodeId: string): void {
  const database = getDb();
  const row = database
    .prepare("SELECT attempts FROM failed_episodes WHERE series_id = ? AND id = ?")
    .get(seriesId, episodeId) as { attempts: number } | undefined;
  const attempts = (row?.attempts ?? 0) + 1;
  const delayDays = Math.min(MAX_RETRY_BACKOFF_DAYS, 2 ** (attempts - 1));
  database
    .prepare(`
      INSERT INTO failed_episodes (series_id, id, attempts, next_retry_at) VALUES (?, ?, ?, ?)
      ON CONFLICT(series_id, id) DO UPDATE SET attempts = excluded.attempts, next_retry_at = excluded.next_retry_at
    `)
    .run(seriesId, episodeId, attempts, Date.now() + delayDays * DAY_MS);
}

/** failed episodes whose retry window has not yet passed */
function readBlockedEpisodeIds(seriesId: string): Set<string> {
  const rows = getDb()
    .prepare("SELECT id FROM failed_episodes WHERE series_id = ? AND next_retry_at > ?")
    .all(seriesId, Date.now()) as { id: string }[];
  return new Set(rows.map((row) => row.id));
}

/**
 * Delete series nobody has requested for `maxAgeMs`. last_fetched_at
 * renews on any request once the hourly freshness window has passed, so
 * it tracks "last requested" closely enough for garbage collection.
 */
function deleteStaleSeries(maxAgeMs: number): number {
  const database = getDb();
  const cutoff = Date.now() - maxAgeMs;
  const stale = database
    .prepare("SELECT id FROM series WHERE last_fetched_at < ?")
    .all(cutoff) as { id: string }[];

  if (stale.length === 0) {
    return 0;
  }

  database.exec("BEGIN");
  try {
    const deleteEpisodes = database.prepare("DELETE FROM episodes WHERE series_id = ?");
    const deleteFailed = database.prepare("DELETE FROM failed_episodes WHERE series_id = ?");
    const deleteSeries = database.prepare("DELETE FROM series WHERE id = ?");
    for (const { id } of stale) {
      deleteEpisodes.run(id);
      deleteFailed.run(id);
      deleteSeries.run(id);
      bumpDataVersion(id);
    }
    database.exec("COMMIT");
  } catch (error) {
    database.exec("ROLLBACK");
    console.error(`Garbage collection failed: ${error}`);
    return 0;
  }
  return stale.length;
}

export type SeriesOverview = {
  id: string;
  title: string;
  episodeCount: number;
  backlogComplete: boolean;
  lastFetchedAt: Date;
  catalogKind: string | null;
};

/** everything the admin dashboard's series table needs, one query */
function listSeriesOverview(): SeriesOverview[] {
  const rows = getDb().prepare(`
    SELECT s.id, s.title, s.backlog_complete, s.last_fetched_at, s.catalog_kind,
           (SELECT COUNT(*) FROM episodes e WHERE e.series_id = s.id) AS episode_count
    FROM series s
    ORDER BY s.last_fetched_at DESC
  `).all() as {
    id: string;
    title: string;
    backlog_complete: number;
    last_fetched_at: number;
    catalog_kind: string | null;
    episode_count: number;
  }[];
  return rows.map((row) => ({
    id: row.id,
    title: row.title,
    episodeCount: row.episode_count,
    backlogComplete: row.backlog_complete === 1,
    lastFetchedAt: new Date(row.last_fetched_at),
    catalogKind: row.catalog_kind,
  }));
}

export type FailedEpisodeRow = {
  seriesId: string;
  episodeId: string;
  attempts: number;
  nextRetryAt: Date;
};

function listFailedEpisodes(limit = 50): FailedEpisodeRow[] {
  const rows = getDb()
    .prepare("SELECT series_id, id, attempts, next_retry_at FROM failed_episodes ORDER BY next_retry_at LIMIT ?")
    .all(limit) as { series_id: string; id: string; attempts: number; next_retry_at: number }[];
  return rows.map((row) => ({
    seriesId: row.series_id,
    episodeId: row.id,
    attempts: row.attempts,
    nextRetryAt: new Date(row.next_retry_at),
  }));
}

function countEpisodes(): number {
  const row = getDb().prepare("SELECT COUNT(*) AS n FROM episodes").get() as { n: number };
  return row.n;
}

function databaseSizeBytes(): number {
  const database = getDb();
  const pageCount = (database.prepare("PRAGMA page_count").get() as { page_count: number }).page_count;
  const pageSize = (database.prepare("PRAGMA page_size").get() as { page_size: number }).page_size;
  return pageCount * pageSize;
}

/** admin action: delete one series and everything belonging to it */
function deleteSeriesById(seriesId: string): void {
  const database = getDb();
  database.exec("BEGIN");
  try {
    database.prepare("DELETE FROM episodes WHERE series_id = ?").run(seriesId);
    database.prepare("DELETE FROM failed_episodes WHERE series_id = ?").run(seriesId);
    database.prepare("DELETE FROM series WHERE id = ?").run(seriesId);
    database.exec("COMMIT");
    bumpDataVersion(seriesId);
  } catch (error) {
    database.exec("ROLLBACK");
    console.error(`Failed to delete series ${seriesId}: ${error}`);
  }
}

/** admin action: mark a series stale so the next request refreshes it */
function expireSeries(seriesId: string): void {
  getDb().prepare("UPDATE series SET last_fetched_at = 0 WHERE id = ?").run(seriesId);
}

export const storage = {
  hasSeries,
  readSeries,
  listSeriesOverview,
  listFailedEpisodes,
  countEpisodes,
  databaseSizeBytes,
  deleteSeriesById,
  expireSeries,
  writeSeries,
  addEpisodes,
  readEpisodeIds,
  touchLastFetched,
  setBacklogState,
  getDataVersion,
  recordEpisodeFailure,
  readBlockedEpisodeIds,
  deleteStaleSeries,
};
