// Progressive enhancement for Kringkasting. Every page works without this
// script: the server renders search results and all subscribe links. This
// adds search-as-you-type, the subscribe dialog with a remembered app, a
// QR code to continue on the phone, and share/copy buttons. Event delegation keeps it working for results
// inserted later.

const STORAGE_KEY = "kringkasting:podcast-app";
// feeds sent to an app from here: { "/api/feeds/<id>": { app, name } }
const SENT_KEY = "kringkasting:sent";
const CHANGE_EVENT = "kringkasting:podcast-app-changed";
const DEBOUNCE_MS = 300;
const MIN_QUERY_LENGTH = 2;

const ICONS = {
  chevron:
    '<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 9l6 6l6 -6"></path></svg>',
  check:
    '<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12l5 5l10 -10"></path></svg>',
  checkDraw:
    '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path class="check-draw" pathLength="1" d="M5 12l5 5l10 -10"></path></svg>',
  signalBars: '<svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true">' +
    [0, 1, 2, 3].map((bar) =>
      `<rect class="eq-bar" style="transform-origin:center bottom;animation-delay:${bar * 150}ms" x="${
        3 + bar * 5.2
      }" y="4" width="3" height="16" rx="1.5" fill="currentColor"></rect>`
    ).join("") + "</svg>",
};

const PRIMARY_BUTTON =
  "relative inline-flex items-center gap-1.5 rounded-lg text-sm font-semibold bg-ink text-canvas hover:bg-ink/80 dark:bg-ink-dark dark:text-canvas-dark dark:hover:bg-ink-dark/85 transition-colors cursor-pointer";

function readChoice() {
  try {
    return localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

function writeChoice(id) {
  try {
    localStorage.setItem(STORAGE_KEY, id);
  } catch {
    // storage unavailable (private mode): the choice lasts for this page
  }
  dispatchEvent(new Event(CHANGE_EVENT));
}

function readSent() {
  try {
    return JSON.parse(localStorage.getItem(SENT_KEY) ?? "{}") ?? {};
  } catch {
    return {};
  }
}

/** remember that this feed was sent to an app, in this browser only */
function recordSent(feedUrl, app) {
  const sent = readSent();
  sent[new URL(feedUrl, location.href).pathname] = { app: app.dataset.app, name: app.dataset.appName };
  try {
    localStorage.setItem(SENT_KEY, JSON.stringify(sent));
  } catch {
    // storage unavailable: nothing to remember
  }
}

/** "Sendt til Overcast" under a card whose feed went to an app */
function renderSent(control) {
  const line = control.closest("article")?.querySelector("[data-subscribed]");
  if (!line) return;
  const entry = readSent()[new URL(control.dataset.feedUrl, location.href).pathname];
  line.hidden = !entry;
  if (!entry) return;
  line.innerHTML = ICONS.check;
  line.append(` Sendt til ${entry.name}`);
}

// ---------------------------------------------------------------- subscribe

// a phone lists only the apps that exist for it; computers list them all
// (iPadOS reports itself as a Mac, but with touch; Macs have none)
const PLATFORM = /Android/i.test(navigator.userAgent)
  ? "android"
  : /iPhone|iPad|iPod/.test(navigator.userAgent) ||
      (/Macintosh/.test(navigator.userAgent) && navigator.maxTouchPoints > 0)
  ? "ios"
  : null;

// a computer: most apps work differently there (data-desktop on the row)
const COMPUTER = PLATFORM === null;
const MAC = /Mac/.test(navigator.userAgent);
const SYSTEM = MAC ? "mac" : /Windows/.test(navigator.userAgent) ? "windows" : "linux";
const PASTE_KEYS = MAC ? "⌘V" : "Ctrl+V";

function availableHere(app) {
  return COMPUTER || (app.dataset.platforms ?? "").split(" ").includes(PLATFORM);
}

/**
 * What an app's row does here: "open" follows the link, "copy" and
 * "copy-open" copy the feed link and explain where to paste it, "phone"
 * (an app only on phones) shows the QR code. Phones always open.
 */
function desktopAction(app) {
  if (!COMPUTER) return "open";
  const rules = Object.fromEntries((app.dataset.desktop ?? "").split(" ").filter(Boolean).map((rule) => rule.split(":")));
  return rules[SYSTEM] ?? rules["*"] ?? "phone";
}

const copies = (app) => desktopAction(app).startsWith("copy");

/** where to paste the link, per app */
function pasteSteps(app) {
  const name = app.dataset.appName;
  switch (app.dataset.app) {
    case "apple":
      return MAC
        ? ["Åpne Podkaster. Vinduet «Følg et program via URL» åpnes.", `Lim inn lenken (${PASTE_KEYS}) og velg Følg.`]
        : ["Åpne Apple Podcasts og velg Fil › Følg et program via URL.", `Lim inn lenken (${PASTE_KEYS}) og velg Følg.`];
    case "pocketcasts":
      return [
        "Åpne Pocket Casts på nett eller i appen på datamaskinen, og gå til Oppdag.",
        `Lim inn lenken (${PASTE_KEYS}) i søkefeltet, velg podkasten og abonner.`,
      ];
  }
  return [`Åpne ${name} og legg til en podkast via URL.`, `Lim inn lenken (${PASTE_KEYS}).`];
}

/** the paste panel's button: opens the app or its website */
function openLabel(app) {
  if (app.dataset.app === "apple" && MAC) return "Åpne Podkaster";
  return app.dataset.desktopHref?.startsWith("https:") ? `Åpne ${app.dataset.appName} på nett` : `Åpne ${app.dataset.appName}`;
}

/** render a card's control: "Abonner i app", or "Åpne i <app>" + chevron */
function renderControl(control) {
  renderSent(control);
  const dialog = control.nextElementSibling;
  if (!(dialog instanceof HTMLDialogElement)) return;
  // a choice made on another platform (e.g. synced) counts as none here
  let chosen = dialog.querySelector(`a[data-app="${CSS.escape(readChoice() ?? "")}"]`);
  // an app that only exists on phones is no choice on a computer
  if (chosen && (!availableHere(chosen) || desktopAction(chosen) === "phone")) chosen = null;
  const pulse = '<span class="pulse-ring border-ink/60 dark:border-ink-dark/60" aria-hidden="true" hidden></span>';

  if (!chosen) {
    const podcast = control.dataset.defaultIcon ?? control.querySelector("button svg")?.outerHTML ?? "";
    control.dataset.defaultIcon = podcast;
    control.innerHTML = `<button type="button" data-open-dialog="${dialog.id}" data-pulse class="${PRIMARY_BUTTON} px-3 py-2">${podcast} Abonner i app${pulse}</button>`;
    return;
  }
  if (!control.dataset.defaultIcon) {
    control.dataset.defaultIcon = control.querySelector("button svg")?.outerHTML ?? "";
  }
  const icon = chosen.querySelector("svg").cloneNode(true);
  icon.setAttribute("width", "15");
  icon.setAttribute("height", "15");
  // says what it does: an app that needs the link pasted gets a copy
  // button, the others a link that opens the app
  const copying = copies(chosen);
  const link = document.createElement(copying ? "button" : "a");
  if (copying) link.type = "button";
  else link.href = chosen.getAttribute("href");
  link.dataset.chosenApp = chosen.dataset.app;
  link.dataset.pulse = "";
  link.dataset.umamiEvent = `app-${chosen.dataset.app}`;
  link.dataset.umamiEventApp = chosen.dataset.appName;
  link.dataset.umamiEventHandling = "aapne";
  link.className = `${PRIMARY_BUTTON} rounded-r-none px-3 py-2`;
  link.append(icon, copying ? ` Kopier lenke til ${chosen.dataset.appName}` : ` Åpne i ${chosen.dataset.appName}`);
  link.insertAdjacentHTML("beforeend", pulse);
  const switcher = document.createElement("button");
  switcher.type = "button";
  switcher.dataset.openDialog = dialog.id;
  switcher.setAttribute("aria-label", "Velg en annen podkast-app");
  switcher.className =
    `${PRIMARY_BUTTON} rounded-l-none border-l border-canvas/25 dark:border-canvas-dark/25 px-2 py-2`;
  switcher.innerHTML = ICONS.chevron;
  control.replaceChildren(link, switcher);
}

function renderControls(root = document) {
  root.querySelectorAll("[data-subscribe]").forEach(renderControl);
}

/** restart the pulse-ring animation on the tapped control */
function pulse(element) {
  const ring = element.querySelector(".pulse-ring");
  if (!ring) return;
  const fresh = ring.cloneNode();
  fresh.hidden = false;
  ring.replaceWith(fresh);
}

function feedUrlOf(element) {
  return element.closest("dialog")?.previousElementSibling?.dataset.feedUrl ??
    element.closest("[data-subscribe]")?.dataset.feedUrl ?? "";
}

// screen readers only announce changes to a live region that already
// exists, so it is added up front; the visible toast stays out of the
// accessibility tree to avoid announcing twice
const hintStatus = document.createElement("p");
hintStatus.setAttribute("role", "status");
hintStatus.className = "sr-only";
document.body.append(hintStatus);

let hintTimer;
function showHint(text) {
  // cleared first, so the same message repeated is announced again
  hintStatus.textContent = "";
  setTimeout(() => (hintStatus.textContent = text), 50);
  let hint = document.getElementById("hint");
  if (!hint) {
    hint = document.createElement("div");
    hint.id = "hint";
    hint.setAttribute("aria-hidden", "true");
    hint.className =
      "fixed inset-x-0 bottom-4 z-50 mx-auto w-fit max-w-[90vw] rounded-xl bg-ink text-canvas dark:bg-ink-dark dark:text-canvas-dark px-4 py-3 text-sm shadow-lg text-center";
    document.body.append(hint);
  }
  hint.textContent = text;
  hint.hidden = false;
  clearTimeout(hintTimer);
  hintTimer = setTimeout(() => (hint.hidden = true), 7000);
}

/**
 * Copy text; true only if it worked. The old synchronous copy runs first,
 * while the click still counts as a user gesture, which Safari requires;
 * the clipboard API is the fallback. container: where the temporary field
 * goes (inside an open modal dialog, the rest of the page is inert).
 */
async function copyText(text, container = document.body) {
  const field = document.createElement("textarea");
  field.value = text;
  field.setAttribute("readonly", "");
  field.style.position = "fixed";
  field.style.opacity = "0";
  container.append(field);
  field.select();
  let copied = false;
  try {
    copied = document.execCommand("copy");
  } catch {
    // not supported: try the clipboard API
  } finally {
    field.remove();
  }
  if (copied) return true;
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // no clipboard API (plain HTTP) or no permission
    return false;
  }
}

/**
 * The panel for an app that needs the link pasted (Apple Podcasts on a
 * computer opens «Følg et program via URL» empty): it says whether the
 * copy worked, keeps the link selectable, and opens the app as a separate
 * step, so the copy never has to survive the app launching.
 */
function pastePanel(dialog) {
  let panel = dialog.querySelector("[data-paste-panel]");
  if (panel) return panel;
  const back = dialog.querySelector("[data-qr-back]").cloneNode(true);
  delete back.dataset.qrBack;
  back.dataset.pasteBack = "";
  panel = document.createElement("div");
  panel.className = "p-2";
  panel.dataset.pastePanel = "";
  panel.hidden = true;
  panel.innerHTML = `<div class="flex items-center gap-1 px-1 pt-1 pb-2"><h2 id="${dialog.id}-paste-title" class="text-sm font-semibold text-ink-2 dark:text-ink-2-dark"></h2></div>
<p role="status" data-paste-status class="mx-3 flex items-center gap-2 text-base font-medium"></p>
<div class="mx-3 mt-2"><input readonly data-paste-url aria-label="RSS-lenke" class="w-full rounded-lg border border-edge dark:border-edge-dark bg-canvas dark:bg-canvas-dark px-2.5 py-2 text-sm"/></div>
<ol class="mx-3 mt-4 list-decimal space-y-1 pl-5 text-sm text-ink-2 dark:text-ink-2-dark"><li data-paste-step-open></li><li data-paste-step-paste></li></ol>
<a data-paste-open class="mt-4 flex w-full items-center justify-center min-h-12 rounded-lg bg-ink text-canvas hover:bg-ink/80 dark:bg-ink-dark dark:text-canvas-dark dark:hover:bg-ink-dark/85 text-base font-semibold transition-colors"></a>
<button type="button" data-close-dialog class="mt-1 flex w-full items-center justify-center min-h-12 rounded-lg text-base font-medium text-ink-2 dark:text-ink-2-dark hover:bg-hover dark:hover:bg-hover-dark transition-colors cursor-pointer">Lukk</button>`;
  panel.firstElementChild.prepend(back);
  dialog.append(panel);
  return panel;
}

/** copy the feed link for an app that needs it pasted, then explain */
async function startPaste(dialog, app) {
  const feedUrl = feedUrlOf(app);
  // copy first, while this is still the click
  const copying = copyText(feedUrl, dialog.open ? dialog : document.body);
  const panel = pastePanel(dialog);
  panel.querySelector("h2").textContent = app.dataset.appName;
  panel.querySelector("[data-paste-url]").value = feedUrl;
  const [first, second] = pasteSteps(app);
  panel.querySelector("[data-paste-step-open]").textContent = first;
  panel.querySelector("[data-paste-step-paste]").textContent = second;
  // a button to open the app or site, where a link can
  const open = panel.querySelector("[data-paste-open]");
  open.hidden = desktopAction(app) !== "copy-open";
  open.href = app.dataset.desktopHref ?? app.getAttribute("href");
  if (open.href.startsWith("https:")) {
    open.target = "_blank";
    open.rel = "noopener";
  } else {
    open.removeAttribute("target");
  }
  open.dataset.pasteApp = app.dataset.app;
  open.textContent = openLabel(app);
  const status = panel.querySelector("[data-paste-status]");
  status.textContent = "";
  if (!dialog.open) openDialog(dialog);
  showView(dialog, "paste");
  if (await copying) {
    status.innerHTML = ICONS.checkDraw;
    status.append("RSS-lenken er kopiert");
    (open.hidden ? panel.querySelector("[data-close-dialog]") : open).focus();
  } else {
    status.textContent = `Kunne ikke kopiere automatisk. Marker lenken og kopier den (${MAC ? "⌘C" : "Ctrl+C"}):`;
    const field = panel.querySelector("[data-paste-url]");
    field.focus();
    field.select();
  }
}

// a computer: the QR code hands the subscription over to the phone
const POINTER_DEVICE = matchMedia("(hover: hover) and (pointer: fine)");

/** show one view of a subscribe dialog: "main" (the apps), "qr", "paste" */
function showView(dialog, view) {
  dialog.querySelector("[data-dialog-main]").hidden = view !== "main";
  dialog.querySelector("[data-qr-panel]").hidden = view !== "qr";
  const paste = dialog.querySelector("[data-paste-panel]");
  if (paste) paste.hidden = view !== "paste";
  dialog.setAttribute("aria-labelledby", `${dialog.id}${view === "main" ? "" : `-${view}`}-title`);
  if (view !== "qr") return;
  const image = dialog.querySelector("[data-qr-src]");
  if (!image.getAttribute("src")) image.src = image.dataset.qrSrc;
}

function openDialog(dialog) {
  resetCopy(dialog);
  showView(dialog, "main");
  dialog.querySelector("[data-qr-row]").hidden = !POINTER_DEVICE.matches;
  // on a computer, rows that don't simply open the app say what they do
  dialog.querySelectorAll("a[data-app]").forEach((app) => {
    const note = app.querySelector("[data-desktop-note]");
    if (!note) return;
    const action = desktopAction(app);
    note.textContent = action === "phone" ? "på mobilen" : action.startsWith("copy") ? "kopierer lenken" : "";
    note.hidden = !note.textContent;
  });
  dialog.querySelectorAll("a[data-app]").forEach((app) => (app.closest("li").hidden = !availableHere(app)));
  dialog.showModal();
}

function resetCopy(dialog) {
  const button = dialog.querySelector("[data-copy]");
  if (!button) return;
  button.querySelector("svg").outerHTML = button.dataset.copyIcon ?? button.querySelector("svg").outerHTML;
  button.querySelector("[data-copy-label]").textContent = "Kopier RSS-lenke";
  dialog.querySelector("[data-copy-status]").textContent = "";
  dialog.querySelector("[data-copy-fallback]").hidden = true;
}

async function copyLink(button) {
  const dialog = button.closest("dialog");
  button.dataset.copyIcon ??= button.querySelector("svg").outerHTML;
  if (await copyText(feedUrlOf(button), dialog)) {
    button.querySelector("svg").outerHTML = ICONS.checkDraw;
    button.querySelector("[data-copy-label]").textContent = "Kopiert!";
    dialog.querySelector("[data-copy-status]").textContent = "RSS-lenken er kopiert";
    showHint("RSS-lenken er kopiert. Lim den inn i podkast-appen din, under «Legg til via URL» eller lignende.");
    // leave the confirmation visible before closing
    setTimeout(() => {
      dialog.close();
      resetCopy(dialog);
    }, 1600);
  } else {
    // clipboard unavailable (permissions, non-secure context): keep the
    // dialog open and offer the URL for manual copying
    dialog.querySelector("[data-copy-status]").textContent = "Kopiering feilet";
    dialog.querySelector("[data-copy-fallback]").hidden = false;
  }
}

async function share(button) {
  const { title, url } = button.dataset;
  if (navigator.share) {
    try {
      await navigator.share({ title, url });
    } catch {
      // the user dismissed the sheet
    }
    return;
  }
  try {
    await navigator.clipboard.writeText(url);
    button.dataset.shareIcon ??= button.querySelector("svg").outerHTML;
    button.querySelector("svg").outerHTML = ICONS.check;
    button.querySelector("[data-share-label]").textContent = "Kopiert";
    setTimeout(() => {
      button.querySelector("svg").outerHTML = button.dataset.shareIcon;
      button.querySelector("[data-share-label]").textContent = "Del";
    }, 1500);
  } catch {
    // clipboard unavailable (plain HTTP): offer the link to copy
    prompt("Kopier lenken:", url);
  }
}

document.addEventListener("click", (event) => {
  const target = event.target instanceof Element ? event.target : null;
  if (!target) return;

  // a click on the backdrop targets the dialog element itself
  if (target instanceof HTMLDialogElement) {
    target.close();
    return;
  }

  const opener = target.closest("[data-open-dialog]");
  if (opener) {
    if (opener.hasAttribute("data-pulse")) pulse(opener);
    openDialog(document.getElementById(opener.dataset.openDialog));
    return;
  }

  const chosen = target.closest("[data-chosen-app]");
  if (chosen) {
    pulse(chosen);
    const dialog = chosen.closest("[data-subscribe]").nextElementSibling;
    const app = dialog.querySelector(`a[data-app="${CSS.escape(chosen.dataset.chosenApp)}"]`);
    if (app && copies(app)) {
      startPaste(dialog, app);
      return;
    }
    if (app) recordSent(feedUrlOf(chosen), app);
    // only the line: replacing the link mid-click would cancel its navigation
    renderSent(chosen.closest("[data-subscribe]"));
    return;
  }

  // the paste panel's "Åpne Podkaster": the link opens the app
  const pasteOpen = target.closest("[data-paste-open]");
  if (pasteOpen) {
    const dialog = pasteOpen.closest("dialog");
    const app = dialog.querySelector(`a[data-app="${CSS.escape(pasteOpen.dataset.pasteApp)}"]`);
    if (app) recordSent(feedUrlOf(app), app);
    renderSent(dialog.previousElementSibling);
    return;
  }
  if (target.closest("[data-paste-back]")) {
    const dialog = target.closest("dialog");
    showView(dialog, "main");
    dialog.querySelector(`a[data-app="${CSS.escape(dialog.querySelector("[data-paste-open]").dataset.pasteApp)}"]`)?.focus();
    return;
  }

  const app = target.closest("dialog a[data-app]");
  if (app) {
    const dialog = app.closest("dialog");
    const control = dialog.previousElementSibling;
    // on a computer: an app only on phones goes there by QR code, one
    // that needs the link pasted gets it copied first
    if (desktopAction(app) === "phone") {
      event.preventDefault();
      showView(dialog, "qr");
      dialog.querySelector("[data-qr-back]").focus();
      return;
    }
    if (copies(app)) {
      event.preventDefault();
      writeChoice(app.dataset.app);
      startPaste(dialog, app);
      return;
    }
    recordSent(feedUrlOf(app), app);
    writeChoice(app.dataset.app);
    dialog.close();
    // the opener was replaced by the split control, so native focus
    // restoration has nowhere to go: place focus deliberately. The row is
    // a link; navigation to the deep link continues natively.
    setTimeout(() => control?.querySelector("a, button")?.focus(), 0);
    return;
  }

  if (target.closest("[data-close-dialog]")) {
    target.closest("dialog").close();
    return;
  }
  if (target.closest("[data-qr-open]")) {
    const dialog = target.closest("dialog");
    showView(dialog, "qr");
    dialog.querySelector("[data-qr-back]").focus();
    return;
  }
  if (target.closest("[data-qr-back]")) {
    const dialog = target.closest("dialog");
    showView(dialog, "main");
    dialog.querySelector("[data-qr-open]").focus();
    return;
  }
  const copy = target.closest("[data-copy]");
  if (copy) {
    copyLink(copy);
    return;
  }
  const shareButton = target.closest("[data-share]");
  if (shareButton) share(shareButton);
});

document.addEventListener("focusin", (event) => {
  if (event.target instanceof HTMLInputElement && event.target.closest("[data-copy-fallback], [data-paste-panel]")) {
    event.target.select();
  }
});

// "/" jumps to the search field, unless typing somewhere or in a dialog
document.addEventListener("keydown", (event) => {
  if (event.key !== "/" || event.ctrlKey || event.metaKey || event.altKey) return;
  const target = event.target;
  if (target instanceof HTMLElement && (target.isContentEditable || target.closest("input, textarea, select, dialog"))) {
    return;
  }
  const input = document.querySelector("[data-search-form] input[name=query]");
  if (!input) return;
  event.preventDefault();
  input.focus();
  input.select();
});

// keep every card in sync when a choice is made here or in another tab
addEventListener(CHANGE_EVENT, () => renderControls());
addEventListener("storage", (event) => {
  if (event.key === STORAGE_KEY || event.key === SENT_KEY) renderControls();
});

// ------------------------------------------------------------------ search

function setupSearch(root) {
  const form = root.querySelector("[data-search-form]");
  const input = form.querySelector("input[name=query]");
  const status = root.querySelector("[data-search-status]");
  const icon = root.querySelector("[data-search-icon]");
  const searchIcon = icon.innerHTML;
  let results = root.querySelector("[data-search-results]");
  // the how-it-works section, kept to restore when the query is cleared
  let intro = results.querySelector("section.mt-28") ? results.innerHTML : null;
  let timer;
  let controller = null;
  let latest = 0;

  const setLoading = (loading) => {
    icon.innerHTML = loading ? ICONS.signalBars : searchIcon;
  };

  const replaceResults = (html) => {
    results.innerHTML = html;
    const inner = results.querySelector("[data-search-results]");
    if (inner) results.replaceChildren(...inner.childNodes);
    renderControls(results);
  };

  const skeleton = () =>
    '<section class="mt-10 space-y-10 animate-pulse motion-reduce:animate-none" aria-hidden="true">' +
    '<div class="flex gap-5"><div class="size-24 sm:size-32 shrink-0 rounded-xl bg-ink/5 dark:bg-ink-dark/8"></div><div class="flex-1 pt-1"><div class="h-5 w-1/2 rounded bg-ink/5 dark:bg-ink-dark/8"></div><div class="mt-3 h-4 w-5/6 rounded bg-ink/5 dark:bg-ink-dark/8"></div><div class="mt-5 h-9 w-40 rounded-lg bg-ink/5 dark:bg-ink-dark/8"></div></div></div>'
      .repeat(3) + "</section>";

  async function run(raw) {
    clearTimeout(timer);
    controller?.abort();
    const trimmed = raw.trim();
    history.replaceState(null, "", trimmed === "" ? "/" : `/?query=${encodeURIComponent(trimmed)}`);
    const request = ++latest;

    if (trimmed.length < MIN_QUERY_LENGTH) {
      setLoading(false);
      status.textContent = "";
      if (intro === null) {
        // page was loaded with results: fetch the suggestions once
        const response = await fetch("/api/search/fragment?q=").catch(() => null);
        if (!response?.ok || request !== latest) return;
        replaceResults(await response.text());
        intro = results.innerHTML;
        return;
      }
      replaceResults(intro);
      return;
    }

    controller = new AbortController();
    const { signal } = controller;
    setLoading(true);
    status.textContent = "Søker …";
    // show a skeleton only when there are no results to keep showing
    if (!results.querySelector("article")) replaceResults(skeleton());

    try {
      const response = await fetch(`/api/search/fragment?q=${encodeURIComponent(trimmed)}`, { signal });
      const html = await response.text();
      if (request !== latest) return; // a newer search owns the page
      replaceResults(html);
      status.textContent = response.ok
        ? decodeURIComponent(response.headers.get("X-Search-Status") ?? "")
        : "Søket feilet. Prøv igjen.";
    } catch (error) {
      if (signal.aborted || request !== latest) return;
      console.error(error);
      replaceResults(
        '<section class="mt-14 space-y-1"><h2 class="text-xl font-medium">Søket feilet</h2><p class="text-ink-2 dark:text-ink-2-dark">Prøv igjen om et lite øyeblikk.</p></section>',
      );
      status.textContent = "Søket feilet. Prøv igjen.";
    } finally {
      if (request === latest) setLoading(false);
    }
  }

  form.addEventListener("submit", (event) => {
    event.preventDefault();
    run(input.value);
  });
  input.addEventListener("input", () => {
    clearTimeout(timer);
    timer = setTimeout(() => run(input.value), DEBOUNCE_MS);
  });
  root.addEventListener("click", (event) => {
    const suggestion = event.target instanceof Element && event.target.closest("[data-suggestion]");
    if (!suggestion) return;
    input.value = suggestion.dataset.suggestion;
    run(input.value);
  });
}

renderControls();
document.querySelectorAll("[data-search]").forEach(setupSearch);

// arriving from a scanned QR code: open that feed's subscribe dialog, once
const handoff = location.hash.startsWith("#subscribe-") && document.getElementById(location.hash.slice(1));
if (handoff instanceof HTMLDialogElement) {
  history.replaceState(null, "", location.pathname + location.search);
  openDialog(handoff);
  // focus the sheet, not its first app: opened without a click, the first
  // app would show a focus ring and look preselected
  handoff.tabIndex = -1;
  handoff.focus();
}
