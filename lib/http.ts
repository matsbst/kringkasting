/**
 * Tiny typed JSON GET helper, replacing the previous dependency on
 * https://deno.land/x/kall with a local implementation.
 *
 * Never throws: network errors and timeouts are reported as status 0
 * with a null body, so callers can treat every failure uniformly.
 */

const REQUEST_TIMEOUT_MS = 15_000;

export type GetResult<T> = {
  status: number;
  body: T | null;
};

export async function get<T>(url: string): Promise<GetResult<T>> {
  try {
    const response = await fetch(url, {
      headers: { accept: "application/json" },
      signal: AbortSignal.timeout(REQUEST_TIMEOUT_MS),
    });

    if (!response.ok) {
      // consume the body so the connection can be released
      await response.body?.cancel();
      return { status: response.status, body: null };
    }

    const body = (await response.json()) as T;
    return { status: response.status, body };
  } catch (error) {
    console.error(`GET ${url} failed: ${error}`);
    return { status: 0, body: null };
  }
}
