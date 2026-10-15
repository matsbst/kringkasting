/**
 * Drives headless Chrome through the DevTools protocol to check the Go
 * version's client-side behaviour (web/static/app.js). Temporary, like the
 * rest of tools/baseline: deleted together with Deno.
 *
 *   deno run -A tools/baseline/browser-check.ts http://127.0.0.1:18080
 */

const base = Deno.args[0] ?? "http://127.0.0.1:18080";
const chromePath = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const port = 9333;
const profile = await Deno.makeTempDir();

const chrome = new Deno.Command(chromePath, {
  args: ["--headless=new", "--disable-gpu", `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`, "about:blank"],
  stdout: "null",
  stderr: "null",
}).spawn();

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
let target: { webSocketDebuggerUrl: string } | null = null;
for (let i = 0; i < 50 && !target; i++) {
  try {
    target = await (await fetch(`http://127.0.0.1:${port}/json/new?${base}/`, { method: "PUT" })).json();
  } catch {
    await sleep(200);
  }
}
if (!target) throw new Error("chrome did not start");

const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((resolve) => ws.addEventListener("open", resolve, { once: true }));
let nextId = 0;
const pending = new Map<number, (value: unknown) => void>();
const problems: string[] = [];
ws.addEventListener("message", (event) => {
  const message = JSON.parse(event.data);
  if (message.id !== undefined) pending.get(message.id)?.(message.result);
  if (message.method === "Runtime.exceptionThrown") {
    problems.push(`exception: ${message.params.exceptionDetails.exception?.description ?? message.params.exceptionDetails.text}`);
  }
  if (message.method === "Runtime.consoleAPICalled" && message.params.type === "error") {
    problems.push(`console.error: ${message.params.args.map((a: { value?: string; description?: string }) => a.value ?? a.description).join(" ")}`);
  }
});
const send = (method: string, params: Record<string, unknown> = {}) =>
  new Promise<any>((resolve) => {
    const id = nextId++;
    pending.set(id, resolve);
    ws.send(JSON.stringify({ id, method, params }));
  });
const evaluate = async (expression: string) =>
  (await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true })).result.value;

await send("Runtime.enable");
await send("Page.enable");
// allow clipboard access so the copy paths succeed
await send("Browser.grantPermissions", { permissions: ["clipboardReadWrite", "clipboardSanitizedWrite"], origin: base });

const results: [string, boolean, unknown][] = [];
const check = (name: string, ok: boolean, detail: unknown = "") => results.push([name, ok, detail]);
const load = async (path: string) => {
  await send("Page.navigate", { url: base + path });
  await sleep(1500);
};

// no navigation to app deep links during the test; our handlers run first
const blockNavigation = `addEventListener("click", (e) => { if (e.target.closest("a[href]")) e.preventDefault(); });`;

await load("/");
await evaluate(blockNavigation);
check("how-it-works shown", await evaluate(`!!document.querySelector("section.mt-28")`));

// search as you type
await evaluate(`(() => { const i = document.querySelector("#query"); i.value = "musikk"; i.dispatchEvent(new Event("input", { bubbles: true })); })()`);
await sleep(2500);
check("typing shows results", await evaluate(`document.querySelectorAll("article").length`) === 10,
  await evaluate(`document.querySelectorAll("article").length`));
check("status announced", await evaluate(`document.querySelector("[data-search-status]").textContent`) === "10 treff for musikk",
  await evaluate(`document.querySelector("[data-search-status]").textContent`));
check("url updated", await evaluate(`location.search`) === "?query=musikk");
check("icon restored", await evaluate(`!document.querySelector("[data-search-icon] .eq-bar")`));

// clearing restores the suggestions
await evaluate(`(() => { const i = document.querySelector("#query"); i.value = ""; i.dispatchEvent(new Event("input", { bubbles: true })); })()`);
await sleep(1000);
check("clearing restores how-it-works", await evaluate(`!!document.querySelector("section.mt-28") && !document.querySelector("article")`));
check("url reset", await evaluate(`location.pathname + location.search`) === "/");

// a suggestion chip searches immediately
const chip = await evaluate(`document.querySelector("[data-suggestion]").dataset.suggestion`);
await evaluate(`document.querySelector("[data-suggestion]").click()`);
await sleep(2500);
check("suggestion searches", await evaluate(`document.querySelector("#query").value`) === chip &&
  await evaluate(`document.querySelectorAll("article").length + document.querySelectorAll("h2.text-xl").length`) > 0);

// no-results message
await evaluate(`(() => { const i = document.querySelector("#query"); i.value = "zzqqxxnotfound"; i.form.requestSubmit(); })()`);
await sleep(2500);
check("no results message", (await evaluate(`document.body.innerText`)).includes("Ingen treff for «zzqqxxnotfound»"));

// subscribe: open the dialog, pick an app, every card switches over
await load("/?query=musikk");
await evaluate(blockNavigation);
await evaluate(`localStorage.clear()`);
await evaluate(`document.querySelector("[data-open-dialog]").click()`);
check("dialog opens", await evaluate(`document.querySelector("dialog").open`));
await evaluate(`document.querySelector("dialog a[data-app=overcast]").click()`);
await sleep(300);
check("choice stored", await evaluate(`localStorage.getItem("kringkasting:podcast-app")`) === "overcast");
check("dialog closes", await evaluate(`!document.querySelector("dialog").open`));
check("all cards switch to the chosen app", await evaluate(
  `[...document.querySelectorAll("[data-subscribe]")].every((c) => c.textContent.includes("Åpne i Overcast"))`,
));
check("chosen link targets the feed", (await evaluate(`document.querySelector("[data-chosen-app]").getAttribute("href")`))
  .startsWith("overcast://x-callback-url/add?url=http"));
check("focus placed on the control", await evaluate(`!!document.activeElement.closest("[data-subscribe]")`));

// the choice survives a reload, and the chevron reopens the dialog
await load("/?query=musikk");
await evaluate(blockNavigation);
check("choice remembered after reload", await evaluate(`document.querySelector("[data-subscribe]").textContent.includes("Åpne i Overcast")`));
await evaluate(`document.querySelector("[data-subscribe] button[data-open-dialog]").click()`);
check("chevron reopens dialog", await evaluate(`document.querySelector("dialog").open`));

// copy the feed link
await evaluate(`document.querySelector("dialog [data-copy]").click()`);
await sleep(400);
const copyLabel = await evaluate(`document.querySelector("dialog [data-copy-label]").textContent`);
const copyFallback = await evaluate(`!document.querySelector("dialog [data-copy-fallback]").hidden`);
check("copy succeeds or offers the link", copyLabel === "Kopiert!" || copyFallback, { copyLabel, copyFallback });
await sleep(2000);
check("dialog closes after copying", copyFallback || await evaluate(`!document.querySelector("dialog").open`));

// backdrop click closes
await evaluate(`document.querySelector("[data-subscribe] button[data-open-dialog]").click()`);
await evaluate(`document.querySelector("dialog").click()`);
check("backdrop click closes", await evaluate(`!document.querySelector("dialog").open`));

// share falls back to copying where the Web Share API is missing
await evaluate(`document.querySelector("[data-share]").click()`);
await sleep(300);
const shareLabel = await evaluate(`document.querySelector("[data-share-label]").textContent`);
check("share works", shareLabel === "Kopiert" || await evaluate(`typeof navigator.share === "function"`), shareLabel);

check("no JavaScript errors", problems.length === 0, problems);

ws.close();
chrome.kill();
await chrome.status;
await Deno.remove(profile, { recursive: true }).catch(() => {});

let failed = 0;
for (const [name, ok, detail] of results) {
  if (!ok) failed++;
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${ok ? "" : ` ${JSON.stringify(detail)}`}`);
}
Deno.exit(failed ? 1 : 0);
