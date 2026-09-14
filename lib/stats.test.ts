import { assertEquals } from "@std/assert";
import { forTestingOnly } from "./stats.ts";

Deno.test("health failures: network/5xx/429/403 count, 2xx/3xx/404 don't", () => {
  for (const status of [0, 500, 503, 429, 403]) {
    assertEquals(forTestingOnly.isHealthFailure(status), true, `status ${status}`);
  }
  for (const status of [200, 204, 301, 404, 400]) {
    assertEquals(forTestingOnly.isHealthFailure(status), false, `status ${status}`);
  }
});

Deno.test("consecutive failures accumulate; any reachable response resets", () => {
  forTestingOnly.reset();
  for (let i = 0; i < 5; i++) {
    forTestingOnly.feedStatus(503);
  }
  assertEquals(forTestingOnly.consecutiveFailures(), 5);

  // a 404 is still a response from NRK (reachable), so the run clears —
  // the alert is for sustained *unreachability*, not for not-founds
  forTestingOnly.feedStatus(404);
  assertEquals(forTestingOnly.consecutiveFailures(), 0);

  for (let i = 0; i < 3; i++) {
    forTestingOnly.feedStatus(0);
  }
  assertEquals(forTestingOnly.consecutiveFailures(), 3);
  forTestingOnly.feedStatus(200);
  assertEquals(forTestingOnly.consecutiveFailures(), 0);
});
