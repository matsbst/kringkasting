import { assertEquals, assertExists } from "@std/assert";
import { captureException, errorReportingEnabled, forTestingOnly } from "./errors.ts";

Deno.test("parseDsn extracts endpoint and key, rejects malformed", () => {
  const ok = forTestingOnly.parseDsn("https://abc123@bugsink.example.com/4");
  assertExists(ok);
  assertEquals(ok.endpoint, "https://bugsink.example.com/api/4/store/");
  assertEquals(ok.publicKey, "abc123");

  assertEquals(forTestingOnly.parseDsn("not-a-url"), null);
  assertEquals(forTestingOnly.parseDsn("https://bugsink.example.com/4"), null); // no key
  assertEquals(forTestingOnly.parseDsn("https://abc@bugsink.example.com/"), null); // no project
});

Deno.test("stack frames are oldest-first with parsed locations", () => {
  const stack = [
    "Error: boom",
    "    at inner (file:///app/lib/a.ts:10:5)",
    "    at outer (file:///app/lib/b.ts:20:3)",
  ].join("\n");
  const frames = forTestingOnly.parseStack(stack);
  assertEquals(frames.length, 2);
  assertEquals(frames[0].function, "outer");
  assertEquals(frames[1].function, "inner");
  assertEquals(frames[1].lineno, 10);
});

Deno.test("reporting is a no-op and never throws when DSN is unset", () => {
  Deno.env.delete("SENTRY_DSN");
  assertEquals(errorReportingEnabled(), false);
  // must not throw
  captureException(new Error("should be swallowed"));
});

Deno.test("captured event scrubs to exception + tags, no PII", async () => {
  // exercise send() directly through a stubbed fetch by enabling a DSN
  // in a fresh module import so getDsn() re-reads the env
  Deno.env.set("SENTRY_DSN", "https://key@bugsink.test/9");
  const mod = await import("./errors.ts?scrub-test");
  const original = globalThis.fetch;
  type Captured = { url: string; body: string; auth: string };
  let captured: Captured | undefined;
  globalThis.fetch = ((input: Request | URL | string, init?: RequestInit) => {
    captured = {
      url: String(input),
      body: String(init?.body ?? ""),
      auth: new Headers(init?.headers).get("x-sentry-auth") ?? "",
    };
    return Promise.resolve(new Response("{}", { status: 200 }));
  }) as typeof fetch;

  try {
    mod.captureException(new Error("kaboom"), { tags: { path: "/api/feeds/x" } });
    await new Promise((resolve) => setTimeout(resolve, 20));
    assertExists(captured);
    assertEquals(captured!.url, "https://bugsink.test/api/9/store/");
    assertEquals(captured!.auth.includes("sentry_key=key"), true);
    const event = JSON.parse(captured!.body);
    assertEquals(event.exception.values[0].value, "kaboom");
    assertEquals(event.tags.path, "/api/feeds/x");
    // no client identity fields
    assertEquals("server_name" in event, false);
    assertEquals("user" in event, false);
  } finally {
    globalThis.fetch = original;
    Deno.env.delete("SENTRY_DSN");
  }
});
