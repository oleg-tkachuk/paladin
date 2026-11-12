import { bannerFromEnv } from "@/lib/banner";

export const dynamic = "force-dynamic";

/**
 * GET /api/banner — the operator's banner, read from the console's own
 * environment. It is UI configuration, holding no token and calling no plane:
 * what the console's server still serves besides the UI (ADR-0048).
 */
export function GET(): Response {
  return Response.json(bannerFromEnv(process.env), {
    headers: { "Cache-Control": "no-store" },
  });
}
