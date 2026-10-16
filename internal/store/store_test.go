package store

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// copyDB copies a database fixture into a temp dir, so tests never write
// to the fixture itself.
func copyDB(t *testing.T, src string) string {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	dst := filepath.Join(t.TempDir(), "kringkasting.sqlite3")
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

type counts struct{ series, episodes, failed, state int }

func countRows(t *testing.T, s *Store) counts {
	t.Helper()
	var c counts
	for table, dst := range map[string]*int{
		"series": &c.series, "episodes": &c.episodes, "failed_episodes": &c.failed, "app_state": &c.state,
	} {
		if err := s.read.QueryRow("SELECT count(*) FROM " + table).Scan(dst); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
	}
	return c
}

func integrityOK(t *testing.T, s *Store) {
	t.Helper()
	var result string
	if err := s.read.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		t.Fatalf("integrity_check = %q, %v", result, err)
	}
}

func TestOpenNewDatabase(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "new.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := countRows(t, s); got != (counts{}) {
		t.Fatalf("new database not empty: %+v", got)
	}
	integrityOK(t, s)
}

func TestOpenDenoCreatedDatabase(t *testing.T) {
	path := copyDB(t, "../../testdata/baseline/deno-created.sqlite3")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := countRows(t, s)
	if want.series == 0 || want.episodes == 0 {
		t.Fatalf("fixture looks empty: %+v", want)
	}
	integrityOK(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// reopening runs the migrations again; they must be no-ops
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := countRows(t, s); got != want {
		t.Fatalf("reopen changed rows: %+v, want %+v", got, want)
	}
}

func TestReadPoolRejectsWrites(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ro.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.read.Exec("INSERT INTO app_state (key, value) VALUES ('x', 'y')"); err == nil {
		t.Fatal("read pool accepted a write")
	}
}

// Optional: KRINGKASTING_PROD_DB points at a copy of a real database (never
// committed) to check that it opens and passes integrity_check.
func TestOpenProductionCopy(t *testing.T) {
	src := os.Getenv("KRINGKASTING_PROD_DB")
	if src == "" {
		t.Skip("set KRINGKASTING_PROD_DB to check a copy of a real database")
	}
	s, err := Open(copyDB(t, src))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := countRows(t, s)
	t.Logf("production copy: %+v", got)
	if got.series == 0 || got.episodes == 0 {
		t.Fatalf("production copy looks empty: %+v", got)
	}
	integrityOK(t, s)
}

// A database path with characters special in URIs opens the file it names.
func TestOpenPathWithURICharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data #1 100% ?")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "kringkasting.sqlite3")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.WriteState("probe", 1)
	s.Close()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("database not at its path: %v", err)
	}
}
