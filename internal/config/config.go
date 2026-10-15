// Package config reads the runtime configuration from the environment.
// The variable names are the self-hosting contract (README, compose.yaml,
// .env.example) and must not change.
package config

import (
	"os"
	"strings"
)

type Config struct {
	// Origin is the public origin (scheme + host, no trailing slash) from
	// APP_ORIGIN or Cloudron's CLOUDRON_APP_ORIGIN; empty means "derive it
	// from the request".
	Origin string
	// AdminToken enables /admin; tokens shorter than 8 characters are
	// treated as unset.
	AdminToken string
	SentryDSN  string
	UmamiSrc   string
	UmamiID    string
	DBPath     string
	// BuildID identifies the release (error reports, admin, asset URLs).
	BuildID string
	Addr    string
}

// FromEnv reads the configuration from the process environment.
func FromEnv() Config {
	return FromLookup(os.Getenv)
}

// FromLookup reads the configuration through `get` (tests pass a map).
func FromLookup(get func(string) string) Config {
	origin := get("APP_ORIGIN")
	if origin == "" {
		origin = get("CLOUDRON_APP_ORIGIN")
	}
	token := get("ADMIN_TOKEN")
	if len(token) < 8 {
		token = ""
	}
	dbPath := get("KRINGKASTING_DB_PATH")
	if dbPath == "" {
		dbPath = "kringkasting.sqlite3"
	}
	// APP_BUILD_ID replaces Deno's DENO_DEPLOYMENT_ID; the old name is
	// still read so existing deployments keep reporting a version
	buildID := get("APP_BUILD_ID")
	if buildID == "" {
		buildID = get("DENO_DEPLOYMENT_ID")
	}
	if buildID == "" {
		buildID = "dev"
	}
	addr := get("LISTEN_ADDR")
	if addr == "" {
		addr = "0.0.0.0:8000"
	}
	return Config{
		Origin:     strings.TrimSuffix(origin, "/"),
		AdminToken: token,
		SentryDSN:  get("SENTRY_DSN"),
		UmamiSrc:   get("UMAMI_SRC"),
		UmamiID:    get("UMAMI_WEBSITE_ID"),
		DBPath:     dbPath,
		BuildID:    buildID,
		Addr:       addr,
	}
}

// ConfiguredOriginHTTPS reports whether a configured origin is HTTPS
// (used for the admin cookie's Secure flag when no proxy header says so).
func (c Config) ConfiguredOriginHTTPS() (configured, https bool) {
	if c.Origin == "" {
		return false, false
	}
	return true, strings.HasPrefix(c.Origin, "https:")
}
