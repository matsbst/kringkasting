// Admin passkeys: registration on the dashboard, login on the login page.
// The server's options are JSON with base64url fields; WebAuthn wants
// ArrayBuffers, and the answers go back as base64url again.

const fromB64 = (value) => {
  const base64 = value.replace(/-/g, "+").replace(/_/g, "/");
  return Uint8Array.from(atob(base64.padEnd(Math.ceil(base64.length / 4) * 4, "=")), (c) => c.charCodeAt(0)).buffer;
};
const toB64 = (buffer) =>
  btoa(String.fromCharCode(...new Uint8Array(buffer))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");

function serialize(credential) {
  // the browser's own serialization, where it has one
  if (typeof credential.toJSON === "function") return credential.toJSON();
  const response = credential.response;
  const out = { id: credential.id, rawId: toB64(credential.rawId), type: credential.type, response: {} };
  for (const key of ["clientDataJSON", "attestationObject", "authenticatorData", "signature", "userHandle"]) {
    if (response[key]) out.response[key] = toB64(response[key]);
  }
  if (typeof response.getTransports === "function") out.response.transports = response.getTransports();
  out.clientExtensionResults = credential.getClientExtensionResults?.() ?? {};
  return out;
}

async function post(path, body) {
  const response = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.message || "Noe gikk galt");
  return data;
}

function say(text) {
  const status = document.querySelector("[data-passkey-status]");
  if (!status) return;
  status.textContent = text;
  status.hidden = !text;
}

// a dismissed or timed-out prompt isn't an error worth a message
const cancelled = (error) => error?.name === "NotAllowedError" || error?.name === "AbortError";

async function register(form) {
  say("");
  try {
    const { publicKey } = await post("/admin/passkey/register/begin", { name: form.elements.name.value });
    publicKey.challenge = fromB64(publicKey.challenge);
    publicKey.user.id = fromB64(publicKey.user.id);
    for (const excluded of publicKey.excludeCredentials ?? []) excluded.id = fromB64(excluded.id);
    const credential = await navigator.credentials.create({ publicKey });
    const result = await post("/admin/passkey/register/finish", serialize(credential));
    location.href = "/admin?m=" + encodeURIComponent(result.message);
  } catch (error) {
    if (error?.name === "InvalidStateError") say("Denne enheten har allerede en passnøkkel her.");
    else if (!cancelled(error)) say(error.message);
  }
}

async function login() {
  say("");
  try {
    const { publicKey } = await post("/admin/passkey/login/begin");
    publicKey.challenge = fromB64(publicKey.challenge);
    for (const allowed of publicKey.allowCredentials ?? []) allowed.id = fromB64(allowed.id);
    const credential = await navigator.credentials.get({ publicKey });
    await post("/admin/passkey/login/finish", serialize(credential));
    location.href = "/admin";
  } catch (error) {
    if (!cancelled(error)) say(error.message);
  }
}

if (window.PublicKeyCredential) {
  document.querySelector("[data-passkey-register]")?.addEventListener("submit", (event) => {
    event.preventDefault();
    register(event.currentTarget);
  });
  document.querySelector("[data-passkey-login]")?.addEventListener("click", login);
} else {
  say("Denne nettleseren støtter ikke passnøkler.");
  for (const element of document.querySelectorAll("[data-passkey-register] button, [data-passkey-login]")) {
    element.disabled = true;
  }
}
