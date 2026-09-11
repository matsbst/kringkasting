import { assertEquals, assertNotEquals } from "@std/assert";
import {
  etagFor,
  etagMatches,
  isNotModifiedSince,
  isValidResourceId,
  responseJSON,
  responseXML,
  withCacheHeaders,
} from "./utils.ts";

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

Deno.test("cache headers include public and max-age", () => {
  const response = withCacheHeaders(responseJSON({}, 200), { maxAge: 3600 });
  assertEquals(response.headers.get("Cache-Control"), "public, max-age=3600");
  assertEquals(response.headers.has("Expires"), true);
});

Deno.test("cache headers include s-maxage for shared caches when given", () => {
  const response = withCacheHeaders(responseJSON({}, 200), { maxAge: 1800, sMaxAge: 3600 });
  assertEquals(response.headers.get("Cache-Control"), "public, max-age=1800, s-maxage=3600");
});

Deno.test("isNotModifiedSince compares with second precision", () => {
  const lastModified = new Date("2024-06-01T12:00:00.500Z");
  assertEquals(isNotModifiedSince("Sat, 01 Jun 2024 12:00:00 GMT", lastModified), true);
  assertEquals(isNotModifiedSince("Sat, 01 Jun 2024 12:00:01 GMT", lastModified), true);
  assertEquals(isNotModifiedSince("Sat, 01 Jun 2024 11:59:59 GMT", lastModified), false);
  assertEquals(isNotModifiedSince("not a date", lastModified), false);
  assertEquals(isNotModifiedSince(null, lastModified), false);
});

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

Deno.test("etagFor is stable and quoted", async () => {
  const first = await etagFor("<rss>abc</rss>");
  const second = await etagFor("<rss>abc</rss>");
  const other = await etagFor("<rss>def</rss>");
  assertEquals(first, second);
  assertNotEquals(first, other);
  assertEquals(first.startsWith('"') && first.endsWith('"'), true);
});

Deno.test("etagMatches handles lists, weak validators and wildcard", async () => {
  const etag = await etagFor("x");
  assertEquals(etagMatches(etag, etag), true);
  assertEquals(etagMatches(`"nope", ${etag}`, etag), true);
  assertEquals(etagMatches(`W/${etag}`, etag), true);
  assertEquals(etagMatches("*", etag), true);
  assertEquals(etagMatches('"nope"', etag), false);
  assertEquals(etagMatches(null, etag), false);
});
