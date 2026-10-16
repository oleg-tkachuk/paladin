import { planeBackendUrls, upstreamFetch } from "@/lib/server/upstream";
import { OAUTH_AUTHORIZE_PATH } from "@/lib/auth/oauthAuthorize";

/**
 * POST /oauth/authorize — the consent page's form, relayed to the IAM plane's
 * Authorization Server (ADR-0009).
 *
 * The page lives on the console's origin, and the AS on the IAM plane's; a
 * deployment that serves them on different hosts left the form posting to a
 * path the console did not have. The console now has it, and passes the form
 * on as the browser sent it. The AS's answer comes back unchanged, its
 * redirect included: a 302 to the client's redirect_uri — often a custom
 * scheme only the browser can follow — or back to this page with an error.
 * The redirect is never followed here.
 *
 * Nothing is stored: the credentials go to the AS in the request body, as
 * they would from the AS's own form.
 */

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const FORM_CONTENT_TYPE = "application/x-www-form-urlencoded";
// The headers of the AS's answer the browser needs: where to go, and what
// the body is.
const RELAYED_HEADERS = ["location", "content-type", "cache-control"];

export async function POST(req: Request): Promise<Response> {
  const upstream = await upstreamFetch(
    planeBackendUrls.iam + OAUTH_AUTHORIZE_PATH,
    {
      method: "POST",
      headers: { "content-type": FORM_CONTENT_TYPE },
      body: await req.text(),
      redirect: "manual",
    },
  );
  const headers = new Headers();
  for (const name of RELAYED_HEADERS) {
    const value = upstream.headers.get(name);
    if (value !== null) headers.set(name, value);
  }
  return new Response(await upstream.arrayBuffer(), {
    status: upstream.status,
    headers,
  });
}
