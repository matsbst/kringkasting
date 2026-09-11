import { assertEquals, assertNotEquals } from "@std/assert";
import { isValidResourceId, responseJSON, responseXML, withExpiry } from "./utils.ts";

Deno.test("JSON response is stringified", async () => {
  const body = { message: "Hello, World!" };
  const response = responseJSON(body, 200);
  assertEquals(await response.text(), JSON.stringify(body));
});

Deno.test("XML resopnse is _not_ stringified", async () => {
  const body = "<message>Hello, World!</message>";
  const response = responseXML(body, 200);
  const responseBody = await response.text();
  assertEquals(responseBody, body);
  assertNotEquals(responseBody, JSON.stringify(body));
});

Deno.test(
  "Response with cache control returns a response with the correct header",
  () => {
    const response = responseJSON({ message: "Hello, World!" }, 200);
    const cachedResponse = withExpiry(response, 3600);
    assertEquals(cachedResponse.headers.get("Cache-Control"), "max-age=3600");
  },
);

Deno.test(
  "Response with cache control returns the same body as the original",
  async () => {
    const body = { message: "Hello, World!" };
    const response = responseJSON(body, 200);
    const cachedResponse = withExpiry(response, 3600);
    assertEquals(await cachedResponse.text(), await response.text());
  },
);

Deno.test("Response with cache control does not modify the original", () => {
  const response = responseJSON({ message: "Hello, World!" }, 200);
  withExpiry(response, 3600);
  assertEquals(response.headers.get("Cache-Control"), null);
});

Deno.test(
  "Response with cache control returns the correct number of seconds",
  () => {
    const response = responseJSON({ message: "Hello, World!" }, 200);
    const ttlInSeconds = 1234;
    const cachedResponse = withExpiry(response, ttlInSeconds);
    assertEquals(
      cachedResponse.headers.get("Cache-Control"),
      `max-age=${ttlInSeconds}`,
    );
  },
);

Deno.test("valid NRK-style resource ids are accepted", () => {
  for (const id of ["oppdatert", "hele_historien", "abels-taarn", "l_0bc5e55a-46b5-48a5-85e5-5a46b5d8a562", "X1"]) {
    assertEquals(isValidResourceId(id), true, id);
  }
});

Deno.test("path-traversal and garbage resource ids are rejected", () => {
  for (const id of ["", "..", "../series", "a/b", "a?x=1", "a#b", "a b", "a%2Fb", "x".repeat(101)]) {
    assertEquals(isValidResourceId(id), false, id);
  }
});
