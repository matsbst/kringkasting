package store

import (
	"bytes"
	"encoding/json"
	"strings"
)

// MarshalJSON encodes like JavaScript's JSON.stringify: no HTML escaping
// of <, > and &, no trailing newline, and U+2028/U+2029 left as-is. Used
// for durable state (which the Deno version must still read) and API
// responses.
func MarshalJSON(value any) (string, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	out := strings.TrimSuffix(buf.String(), "\n")
	// encoding/json always escapes U+2028/U+2029; JSON.stringify doesn't
	out = strings.ReplaceAll(out, `\u2028`, "\u2028")
	return strings.ReplaceAll(out, `\u2029`, "\u2029"), nil
}
