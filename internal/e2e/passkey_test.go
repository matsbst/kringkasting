package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

var b64 = base64.RawURLEncoding

// softKey is a software passkey device: what Face ID and the iCloud
// Keychain do, with an ECDSA P-256 key in memory and no attestation.
type softKey struct {
	key       *ecdsa.PrivateKey
	id        []byte
	userID    []byte
	signCount uint32
}

func newSoftKey(t *testing.T) *softKey {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	return &softKey{key: key, id: id}
}

type publicKeyOptions struct {
	PublicKey struct {
		Challenge string `json:"challenge"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
	} `json:"publicKey"`
}

func (k *softKey) clientData(kind, challenge string) []byte {
	data, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return data
}

func (k *softKey) authData(attested bool) []byte {
	rpIDHash := sha256.Sum256([]byte(strings.TrimPrefix(origin, "https://")))
	data := append([]byte{}, rpIDHash[:]...)
	flags := byte(0x01 | 0x04) // user present, user verified
	if attested {
		flags |= 0x40
	}
	data = append(data, flags)
	data = binary.BigEndian.AppendUint32(data, k.signCount)
	if attested {
		data = append(data, make([]byte, 16)...) // AAGUID
		data = binary.BigEndian.AppendUint16(data, uint16(len(k.id)))
		data = append(data, k.id...)
		x, y := k.key.X.FillBytes(make([]byte, 32)), k.key.Y.FillBytes(make([]byte, 32))
		coseKey, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
		data = append(data, coseKey...)
	}
	return data
}

// create answers navigator.credentials.create.
func (k *softKey) create(t *testing.T, options []byte) []byte {
	var o publicKeyOptions
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatalf("creation options: %v\n%s", err, options)
	}
	k.userID, _ = b64.DecodeString(o.PublicKey.User.ID)
	attestation, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": k.authData(true)})
	body, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(k.clientData("webauthn.create", o.PublicKey.Challenge)),
			"attestationObject": b64.EncodeToString(attestation),
			"transports":        []string{"internal"},
		},
	})
	return body
}

// get answers navigator.credentials.get.
func (k *softKey) get(t *testing.T, options []byte) []byte {
	var o publicKeyOptions
	if err := json.Unmarshal(options, &o); err != nil {
		t.Fatalf("request options: %v\n%s", err, options)
	}
	k.signCount++
	authData := k.authData(false)
	clientData := k.clientData("webauthn.get", o.PublicKey.Challenge)
	clientHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, k.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData),
			"authenticatorData": b64.EncodeToString(authData),
			"signature":         b64.EncodeToString(signature),
			"userHandle":        b64.EncodeToString(k.userID),
		},
	})
	return body
}

// ceremony runs begin and finish against /admin/passkey/{kind}, as
// admin.js does; answer turns the options into the device's response.
func ceremony(t *testing.T, app *instance, kind, cookie string, answer func([]byte) []byte) (*http.Response, string) {
	t.Helper()
	headers := map[string]string{"Content-Type": "application/json"}
	if cookie != "" {
		headers["Cookie"] = cookie
	}
	begin := app.do(request{method: http.MethodPost, path: "/admin/passkey/" + kind + "/begin", headers: headers,
		body: `{"name":"Testnøkkel"}`, remote: "203.0.113.77"})
	options := body(begin)
	if begin.StatusCode != http.StatusOK {
		t.Fatalf("%s begin: %d %s", kind, begin.StatusCode, options)
	}
	ceremonyCookie, _, _ := strings.Cut(begin.Header.Get("Set-Cookie"), ";")
	finishHeaders := map[string]string{"Content-Type": "application/json", "Cookie": ceremonyCookie}
	if cookie != "" {
		finishHeaders["Cookie"] = cookie + "; " + ceremonyCookie
	}
	finish := app.do(request{method: http.MethodPost, path: "/admin/passkey/" + kind + "/finish", headers: finishHeaders,
		body: string(answer([]byte(options))), remote: "203.0.113.77"})
	return finish, ceremonyCookie
}

// Register a passkey while signed in with the token, sign in with it,
// and check what must fail: a replayed or tampered answer, token login
// with ADMIN_PASSKEY_ONLY, and a removed passkey.
func TestAdminPasskeys(t *testing.T) {
	app := start(t, options{env: adminEnv()})
	if strings.Contains(body(app.do(request{path: "/admin"})), "Logg inn med passnøkkel") {
		t.Error("no passkey is registered yet")
	}

	device := newSoftKey(t)
	resp, _ := ceremony(t, app, "register", app.adminCookie(t), func(o []byte) []byte { return device.create(t, o) })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register finish: %d %s", resp.StatusCode, body(resp))
	}
	dashboard := app.admin(t, "/innstillinger")
	if !strings.Contains(dashboard, "Testnøkkel") || !strings.Contains(dashboard, "sist brukt aldri") {
		t.Fatal("the new passkey isn't listed")
	}

	// registering needs a session
	unauthenticated := app.do(request{method: http.MethodPost, path: "/admin/passkey/register/begin",
		headers: map[string]string{"Content-Type": "application/json"}, body: "{}"})
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Errorf("register without a session: %d", unauthenticated.StatusCode)
	}

	if !strings.Contains(body(app.do(request{path: "/admin"})), "Logg inn med passnøkkel") {
		t.Error("the login page must offer the passkey")
	}
	var answer []byte
	resp, ceremonyCookie := ceremony(t, app, "login", "", func(o []byte) []byte { answer = device.get(t, o); return answer })
	session, _, _ := strings.Cut(resp.Header.Get("Set-Cookie"), ";")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(session, "kringkasting_admin=") {
		t.Fatalf("passkey login: %d %q %s", resp.StatusCode, session, body(resp))
	}
	if html := body(app.do(request{path: "/admin/innstillinger", headers: map[string]string{"Cookie": session}})); !strings.Contains(html, "Logg ut") ||
		strings.Contains(html, "sist brukt aldri") {
		t.Error("the passkey session must sign in, and the passkey show its use")
	}

	// a ceremony can be finished once
	replay := app.do(request{method: http.MethodPost, path: "/admin/passkey/login/finish", body: string(answer),
		headers: map[string]string{"Content-Type": "application/json", "Cookie": ceremonyCookie}})
	if replay.StatusCode == http.StatusOK {
		t.Error("a replayed login was accepted")
	}
	// another key's signature isn't the registered passkey's
	impostor := newSoftKey(t)
	impostor.id, impostor.userID = device.id, device.userID
	resp, _ = ceremony(t, app, "login", "", func(o []byte) []byte { return impostor.get(t, o) })
	if resp.StatusCode != http.StatusUnauthorized || strings.Contains(resp.Header.Get("Set-Cookie"), "kringkasting_admin=") {
		t.Errorf("a wrong signature: %d %q", resp.StatusCode, resp.Header.Get("Set-Cookie"))
	}

	// ADMIN_PASSKEY_ONLY: the right token no longer signs in
	app.stop(t)
	env := adminEnv()
	env["ADMIN_PASSKEY_ONLY"] = "true"
	strict := start(t, options{env: env, dbPath: app.dbPath})
	login := strict.do(request{method: http.MethodPost, path: "/admin", remote: "203.0.113.78",
		headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, body: url.Values{"token": {adminToken}}.Encode()})
	if html := body(login); login.StatusCode == http.StatusSeeOther || !strings.Contains(html, "Innlogging med token er slått av") {
		t.Fatalf("token login with ADMIN_PASSKEY_ONLY: %d", login.StatusCode)
	}
	if page := body(strict.do(request{path: "/admin"})); strings.Contains(page, `name="token"`) {
		t.Error("the token field must be gone with ADMIN_PASSKEY_ONLY")
	}
	resp, _ = ceremony(t, strict, "login", "", func(o []byte) []byte { return device.get(t, o) })
	session, _, _ = strings.Cut(resp.Header.Get("Set-Cookie"), ";")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("passkey login with ADMIN_PASSKEY_ONLY: %d %s", resp.StatusCode, body(resp))
	}

	// removing the passkey brings token login back
	html := body(strict.do(request{path: "/admin/innstillinger", headers: map[string]string{"Cookie": session}}))
	id := html[strings.Index(html, `name="passkey" value="`)+len(`name="passkey" value="`):]
	id = id[:strings.Index(id, `"`)]
	strict.do(request{method: http.MethodPost, path: "/admin", body: url.Values{"action": {"passkey-delete"}, "passkey": {id}}.Encode(),
		headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Cookie": session}})
	if page := body(strict.do(request{path: "/admin"})); strings.Contains(page, "Logg inn med passnøkkel") || !strings.Contains(page, `name="token"`) {
		t.Error("with no passkey left, the token must work again")
	}
}

// Without an https address there are no passkeys, and the dashboard says
// why.
func TestAdminPasskeysNeedHTTPS(t *testing.T) {
	env := adminEnv()
	env["APP_ORIGIN"] = "http://192.168.1.20:8000"
	app := start(t, options{env: env})
	if !strings.Contains(app.admin(t, "/innstillinger"), "Passnøkler krever en fast https-adresse") {
		t.Error("the dashboard must explain why passkeys are missing")
	}
	resp := app.do(request{method: http.MethodPost, path: "/admin/passkey/login/begin", body: "{}"})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("passkey routes without https: %d", resp.StatusCode)
	}
}
