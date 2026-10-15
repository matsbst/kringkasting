/**
 * Headers set on every response. Applied by the middleware in main.ts and,
 * because Fresh renders error pages outside the middleware chain, also by
 * routes/_error.tsx.
 */
export const SECURITY_HEADERS: Record<string, string> = {
  "X-Content-Type-Options": "nosniff",
  "Referrer-Policy": "same-origin",
  "X-Frame-Options": "DENY",
};
