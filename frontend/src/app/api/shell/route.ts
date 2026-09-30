import { createClient, ConnectError, Code } from "@connectrpc/connect";
import { toJson, type JsonValue } from "@bufbuild/protobuf";

import {
  HealthService,
  VersionInfoSchema,
  HealthInfoSchema,
} from "@/gen/paladin/iam/v1/health_service_pb";
import {
  PlatformOperationService,
  ListOperationsResponseSchema,
} from "@/gen/paladin/admin/v1/operation_service_pb";
import { SortOrder } from "@/gen/paladin/common/v1/pagination_pb";
import { anyRegistry } from "@/lib/connect/any-registry";
import { planeTransport } from "@/lib/server/upstream";
import { tokenMatchesPlane } from "@/lib/auth/jwtAudience";

/**
 * One call for the console shell's own state.
 *
 * Loading any page used to issue about six RPCs that belonged to the chrome
 * rather than to the page — the version and health badges, the background-ops
 * poller, the scope picker's lists. Every page paid for all of them, so a
 * page's availability was the product of several services: one plane timing
 * out on a TLS handshake was enough to leave a page of 401s, because the
 * token exchange queued behind the same saturated connection pool.
 *
 * The fan-out still happens, but here — inside the cluster, over the pooled
 * connection the bridge already keeps warm, in parallel — and each section
 * reports its own status, so the console can grey one badge instead of
 * failing a page.
 *
 * Two tokens, because the sections live on two planes and each plane verifies
 * its own audience: the admin token in Authorization, the iam token in
 * X-Paladin-Iam-Authorization. Forwarding one token to both planes would make
 * the health badge permanently unavailable with an authentication error —
 * the plane is right to refuse it.
 */

export const dynamic = "force-dynamic";

// The header the console uses to carry its iam-audience token alongside the
// admin one. Not "Authorization" twice: a proxy that mangles duplicates would
// fail in a way nobody could read.
const IAM_AUTH_HEADER = "X-Paladin-Iam-Authorization";

type SectionStatus = "ok" | "unavailable";

interface Section<T> {
  status: SectionStatus;
  // A short, human-readable explanation the console can show in a tooltip.
  // Absent when status is "ok".
  reason?: string;
  data?: T;
}

const iamClient = createClient(HealthService, planeTransport("iam"));
const adminOperations = createClient(
  PlatformOperationService,
  planeTransport("admin"),
);

// A section's failure must never become the response's failure — that is the
// whole point of splitting them.
async function section<T>(
  name: string,
  fn: () => Promise<T>,
): Promise<Section<T>> {
  try {
    return { status: "ok", data: await fn() };
  } catch (err) {
    const reason =
      err instanceof ConnectError
        ? `${Code[err.code]}: ${err.rawMessage}`
        : err instanceof Error
          ? err.message
          : String(err);
    console.warn(`[Shell] ${name} unavailable: ${reason}`);
    return { status: "unavailable", reason };
  }
}

export async function GET(req: Request) {
  // The admin token is optional, like the iam one: a tenant.user is never
  // issued one, and the iam sections are still theirs. With neither there is
  // nobody to answer for.
  const auth = req.headers.get("Authorization");
  if (!auth && !req.headers.get(IAM_AUTH_HEADER)) {
    return Response.json(
      { code: "unauthenticated", message: "missing Authorization" },
      { status: 401 },
    );
  }
  // The same audience gate the RPC bridge applies: this route reaches the
  // same cluster-only planes, so it must not become a way around it.
  if (auth && !tokenMatchesPlane(auth, "admin")) {
    return Response.json(
      {
        code: "permission_denied",
        message: "token audience does not match the requested plane",
      },
      { status: 403 },
    );
  }
  const headers = auth ? { Authorization: auth } : null;

  // The iam half is optional in the sense that its absence degrades two
  // sections rather than the response — but it is never guessed at.
  const iamAuth = req.headers.get(IAM_AUTH_HEADER);
  const iamHeaders = iamAuth ? { Authorization: iamAuth } : null;
  const noIamToken = async () => {
    throw new Error(
      iamAuth
        ? "iam token audience does not match the iam plane"
        : `missing ${IAM_AUTH_HEADER}`,
    );
  };
  const iamUsable = iamHeaders !== null && tokenMatchesPlane(iamAuth, "iam");

  const [version, health, operations] = await Promise.all([
    section("version", async () =>
      iamUsable
        ? toJson(
            VersionInfoSchema,
            await iamClient.getVersion({}, { headers: iamHeaders! }),
          )
        : noIamToken(),
    ),
    section("health", async () =>
      iamUsable
        ? toJson(
            HealthInfoSchema,
            await iamClient.getHealth({}, { headers: iamHeaders! }),
          )
        : noIamToken(),
    ),
    section("operations", async () => {
      if (!headers) throw new Error("missing Authorization (admin plane)");
      return toJson(
        ListOperationsResponseSchema,
        await adminOperations.listOperations(
          {
            page: { pageSize: 50, pageToken: "" },
            filter: "",
            // Newest first. The cursor is ascending by default, so a page of
            // 50 is the 50 OLDEST operations — which is how a three-day-old
            // failure became the drawer's idea of current activity while
            // everything that ran since stayed out of view.
            sortOrder: SortOrder.DESC,
          },
          { headers },
        ),
        // operations.metadata and .response are Any; without the registry the
        // re-encode throws "not in the type registry" and turns a healthy
        // response into a failed section.
        { registry: anyRegistry },
      );
    }),
  ]);

  const body: Record<string, Section<JsonValue>> = {
    version,
    health,
    operations,
  };
  return Response.json(body, {
    // The shell is per-operator and changes constantly; a cached copy would
    // show a stale health badge for as long as the cache lives.
    headers: { "Cache-Control": "no-store" },
  });
}
