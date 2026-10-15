//go:build roundtrip

// The database round trip (docs/go-migration.md, release gate). Opt-in:
//
//	deno task build && go test -tags roundtrip -run TestRoundTrip -v ./internal/e2e/
//
// Needs Deno, the built Fresh app (_fresh/) and a production copy at
// KRINGKASTING_PROD_DB (default ~/Prosjekter/kringkasting-data/kringkasting-copy.sqlite3).
//
//  1. Deno and Go each run the same recorded flows (all baseline feeds,
//     chapters, search, catalog, both failure scenarios' feeds) against
//     their own copy of the production database.
//  2. Every table is dumped and compared field by field: rows Go was not
//     meant to touch must be identical, and rows it wrote must hold what
//     Deno wrote (wall-clock timestamps compared by presence only).
//  3. Deno opens the database Go wrote and serves every feed in it; the
//     XML must equal what Go serves from the same file, and what Deno
//     serves from its own copy.
package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func prodCopy(t *testing.T) string {
	src := os.Getenv("KRINGKASTING_PROD_DB")
	if src == "" {
		home, _ := os.UserHomeDir()
		src = filepath.Join(home, "Prosjekter", "kringkasting-data", "kringkasting-copy.sqlite3")
	}
	if _, err := os.Stat(src); err != nil {
		t.Skip("no production copy at " + src)
	}
	return src
}

func copyFile(t *testing.T, src string) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "kringkasting.sqlite3")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

// deno runs the built Fresh app behind tools/baseline (replayed NRK, pinned
// clock) on dbPath, until stop is called.
type deno struct {
	cmd  *exec.Cmd
	base string
}

func startDeno(t *testing.T, dbPath string, port int) *deno {
	t.Helper()
	if resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/personvern", port)); err == nil {
		resp.Body.Close()
		t.Fatalf("port %d is already in use (a stale server?)", port)
	}
	cmd := exec.Command("deno", "run", "-A", "tools/baseline/serve.ts")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(),
		"NRK_FIXTURES=replay", "FIXTURE_DIR=testdata/nrk", fmt.Sprintf("PORT=%d", port),
		"KRINGKASTING_DB_PATH="+dbPath, "APP_ORIGIN="+origin)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	d := &deno{cmd: cmd, base: fmt.Sprintf("http://127.0.0.1:%d", port)}
	t.Cleanup(d.stop)
	for range 100 {
		if resp, err := http.Get(d.base + "/personvern"); err == nil {
			resp.Body.Close()
			return d
		}
		time.Sleep(100 * time.Millisecond)
	}
	d.stop()
	t.Fatal("deno server did not start")
	return nil
}

func (d *deno) get(t *testing.T, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(d.base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (d *deno) stop() {
	if d.cmd.ProcessState != nil {
		return
	}
	_ = d.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = d.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = d.cmd.Process.Kill()
	}
}

// flows are the requests both versions run; feeds are warmed until stable.
var flowPaths = []string{
	"/", "/?query=musikk", "/api/search?q=musikk",
	"/api/feeds/supertroett/l_dc4c5213-d03c-4491-8c52-13d03cc491a1/chapters",
	"/api/feeds/musikkhistorier/l_6f0c81a3-4e1c-4cee-8c81-a34e1c0cee7c/chapters",
}

func warmUntilStable(t *testing.T, get func(string) (int, string), path string) {
	t.Helper()
	previous := ""
	for range 60 {
		status, b := get(path)
		if status == http.StatusOK && b == previous {
			return
		}
		previous = b
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("%s never stabilised", path)
}

func feedIDs(t *testing.T, dbPath string) []string {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT id FROM series ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids
}

// clockColumns hold wall-clock times; they're compared by presence only.
var clockColumns = map[string]bool{"last_fetched_at": true, "metadata_refreshed_at": true, "next_retry_at": true}
var clockKeys = map[string]bool{"nextRetryAt": true, "expiresAt": true, "expires": true}

// fullDump renders every row of every table, with SQLite storage types,
// ordered by primary key.
func fullDump(t *testing.T, dbPath string) map[string]string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := map[string]string{}
	for table, order := range map[string]string{
		"series": "id", "episodes": "series_id, id", "failed_episodes": "series_id, id", "app_state": "key",
	} {
		rows, err := db.Query("SELECT * FROM " + table + " ORDER BY " + order)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			fields := make([]string, len(columns))
			for i, v := range values {
				fields[i] = normalize(columns[i], v)
			}
			// the primary key prefix identifies the row
			key := table + ":" + fields[0]
			if table == "episodes" || table == "failed_episodes" {
				key += "/" + fields[1]
			}
			out[key] = strings.Join(fields, " | ")
		}
		rows.Close()
	}
	return out
}

func normalize(column string, v any) string {
	if v == nil {
		return "NULL"
	}
	if clockColumns[column] {
		return "<time>"
	}
	switch v := v.(type) {
	case []byte:
		return normalizeJSON(column, string(v))
	case string:
		return normalizeJSON(column, v)
	case int64:
		return fmt.Sprintf("int:%d", v)
	case float64:
		return fmt.Sprintf("real:%v", v)
	}
	return fmt.Sprintf("%T:%v", v, v)
}

// normalizeJSON blanks wall-clock fields inside app_state values.
func normalizeJSON(column, s string) string {
	if column != "value" {
		return "text:" + s
	}
	var value any
	if json.Unmarshal([]byte(s), &value) != nil {
		return "text:" + s
	}
	var walk func(any) any
	walk = func(v any) any {
		switch v := v.(type) {
		case map[string]any:
			for k, inner := range v {
				if clockKeys[k] {
					v[k] = "<time>"
				} else {
					v[k] = walk(inner)
				}
			}
		case []any:
			for i := range v {
				v[i] = walk(v[i])
			}
		}
		return v
	}
	encoded, _ := json.Marshal(walk(value))
	return "json:" + string(encoded)
}

func diffDumps(t *testing.T, label string, got, want map[string]string) {
	t.Helper()
	var problems []string
	for key, w := range want {
		if g, ok := got[key]; !ok {
			problems = append(problems, "missing "+key)
		} else if g != w {
			problems = append(problems, fmt.Sprintf("%s\n   got: %s\n  want: %s", key, g, w))
		}
	}
	for key := range got {
		if _, ok := want[key]; !ok {
			problems = append(problems, "extra "+key)
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		if len(problems) > 30 {
			problems = append(problems[:30], fmt.Sprintf("… and %d more", len(problems)-30))
		}
		t.Errorf("%s: %d differences\n%s", label, len(problems), strings.Join(problems, "\n"))
	}
}

func integrity(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		t.Fatalf("integrity_check %s = %q, %v", dbPath, result, err)
	}
}

func TestRoundTrip(t *testing.T) {
	prod := prodCopy(t)
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not installed")
	}
	before := fullDump(t, prod)
	t.Logf("production copy: %d rows", len(before))

	// 1. Deno writes its copy
	denoDB := copyFile(t, prod)
	d := startDeno(t, denoDB, 8771)
	denoGet := func(path string) (int, string) { return d.get(t, path) }
	for _, id := range feeds {
		warmUntilStable(t, denoGet, "/api/feeds/"+id)
	}
	for _, path := range flowPaths {
		denoGet(path)
	}
	// the homepage only triggers the catalog load; give it time to finish
	time.Sleep(3 * time.Second)
	d.stop()

	// 1. Go writes its copy, same flows
	goDB := copyFile(t, prod)
	g := start(t, options{env: map[string]string{"APP_ORIGIN": origin}, dbPath: goDB})
	goGet := func(path string) (int, string) {
		resp := g.do(request{path: path})
		return resp.StatusCode, body(resp)
	}
	for _, id := range feeds {
		g.warmFeed(t, id)
	}
	for _, path := range flowPaths {
		goGet(path)
	}
	time.Sleep(3 * time.Second)
	g.stop(t)

	integrity(t, denoDB)
	integrity(t, goDB)

	// 2. field-by-field: Go wrote what Deno wrote, and touched nothing else
	denoAfter, goAfter := fullDump(t, denoDB), fullDump(t, goDB)
	diffDumps(t, "Go-written vs Deno-written database", goAfter, denoAfter)
	untouched := 0
	for key, value := range before {
		if denoAfter[key] == value {
			untouched++
			if goAfter[key] != value {
				t.Errorf("Go changed a row Deno left alone: %s", key)
			}
		}
	}
	t.Logf("%d of %d production rows untouched by both; %d rows after", untouched, len(before), len(goAfter))

	// 3. Deno serves the Go-written database: every feed identical
	ids := feedIDs(t, goDB)
	goServed := map[string]string{}
	g2 := start(t, options{env: map[string]string{"APP_ORIGIN": origin}, dbPath: copyFile(t, goDB)})
	for _, id := range ids {
		resp := g2.do(request{path: "/api/feeds/" + id})
		goServed[id] = body(resp)
	}
	g2.cancel()
	g2.cancel = nil
	_ = g2.Close(context.Background())

	d2 := startDeno(t, goDB, 8772)
	defer d2.stop()
	mismatches := 0
	for _, id := range ids {
		status, xml := d2.get(t, "/api/feeds/"+id)
		if status != http.StatusOK || xml != goServed[id] {
			mismatches++
			t.Errorf("feed %s: Deno on the Go-written database served %d and differs from Go (%d vs %d bytes)",
				id, status, len(xml), len(goServed[id]))
		}
	}
	t.Logf("Deno served all %d feeds from the Go-written database; %d mismatches", len(ids), mismatches)
	integrity(t, goDB)
}
