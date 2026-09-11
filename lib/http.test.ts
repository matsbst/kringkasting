import { assertEquals } from "@std/assert";
import { forTestingOnly, get } from "./http.ts";
import { allowRequest } from "./rate-limit.ts";

Deno.test("upstream requests are bounded by the global semaphore", async () => {
  const original = globalThis.fetch;
  let inFlight = 0;
  let maxInFlight = 0;

  globalThis.fetch = (() => {
    inFlight++;
    maxInFlight = Math.max(maxInFlight, inFlight);
    return new Promise<Response>((resolve) =>
      setTimeout(() => {
        inFlight--;
        resolve(new Response("{}", { status: 200, headers: { "content-type": "application/json" } }));
      }, 10)
    );
  }) as typeof fetch;

  try {
    const results = await Promise.all(
      Array.from({ length: 40 }, (_, index) => get(`https://upstream.test/${index}`)),
    );
    assertEquals(results.every((result) => result.status === 200), true);
    assertEquals(
      maxInFlight <= forTestingOnly.MAX_CONCURRENT_UPSTREAM,
      true,
      `max in flight was ${maxInFlight}`,
    );
  } finally {
    globalThis.fetch = original;
  }
});

Deno.test("token bucket drains, refuses, and refills over time", () => {
  const key = "test:" + crypto.randomUUID();
  const start = 1_000_000;

  // burst allowance of 3
  assertEquals(allowRequest(key, 3, 1, start), true);
  assertEquals(allowRequest(key, 3, 1, start), true);
  assertEquals(allowRequest(key, 3, 1, start), true);
  assertEquals(allowRequest(key, 3, 1, start), false);

  // one second later, one token has refilled
  assertEquals(allowRequest(key, 3, 1, start + 1_000), true);
  assertEquals(allowRequest(key, 3, 1, start + 1_000), false);

  // never exceeds capacity after a long idle
  assertEquals(allowRequest(key, 3, 1, start + 60_000), true);
  assertEquals(allowRequest(key, 3, 1, start + 60_000), true);
  assertEquals(allowRequest(key, 3, 1, start + 60_000), true);
  assertEquals(allowRequest(key, 3, 1, start + 60_000), false);
});
