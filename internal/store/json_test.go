package store

import "testing"

// MarshalJSON must encode like JavaScript's JSON.stringify did, so durable
// state and API responses stay byte-compatible.
func TestMarshalJSONLikeJavaScript(t *testing.T) {
	got, err := MarshalJSON(map[string]string{"a": "x\u2028y\u2029<b>&"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"a\":\"x\u2028y\u2029<b>&\"}"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
