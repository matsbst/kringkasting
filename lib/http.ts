/**
 * Tiny typed JSON GET helper, replacing the previous dependency on
 * https://deno.land/x/kall with a local implementation.
 */

export type GetResult<T> = {
  status: number;
  body: T | null;
};

export async function get<T>(url: string): Promise<GetResult<T>> {
  const response = await fetch(url, {
    headers: { accept: "application/json" },
  });

  if (!response.ok) {
    // consume the body so the connection can be released
    await response.body?.cancel();
    return { status: response.status, body: null };
  }

  const body = (await response.json()) as T;
  return { status: response.status, body };
}
