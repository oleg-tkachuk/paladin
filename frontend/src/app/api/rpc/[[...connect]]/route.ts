import {
  createClient,
  createConnectRouter,
  createContextKey,
  createContextValues,
  ConnectError,
} from "@connectrpc/connect";
import { createGrpcWebTransport } from "@connectrpc/connect-web";
// DescService is the descriptor type for any generated Connect service.
// connect-es v2 doesn't re-export it; import directly from protobuf codegenv1.
import type { DescService } from "@bufbuild/protobuf";

import { RPC_API_PREFIX, RPC_PLANE_PREFIXES, type Plane } from "@/constants";
import { tokenMatchesPlane } from "@/lib/auth/jwtAudience";

// admin plane services
import { ObjectKeyService } from "@/gen/paladin/admin/v1/object_key_service_pb";
import { BucketService } from "@/gen/paladin/admin/v1/bucket_service_pb";
import { TenantService } from "@/gen/paladin/admin/v1/tenant_service_pb";
import { PolicyService } from "@/gen/paladin/admin/v1/policy_service_pb";
import { CELService } from "@/gen/paladin/admin/v1/cel_service_pb";
import { BackendService } from "@/gen/paladin/admin/v1/backend_service_pb";
import { QuotaService } from "@/gen/paladin/admin/v1/quota_service_pb";
import { AuditLogService } from "@/gen/paladin/admin/v1/audit_service_pb";
import { EventSubscriptionService } from "@/gen/paladin/admin/v1/event_subscription_service_pb";
import { OperationService as AdminOperationService } from "@/gen/paladin/admin/v1/operation_service_pb";
import { SystemService as AdminSystemService } from "@/gen/paladin/admin/v1/system_service_pb";
import { APITokenService } from "@/gen/paladin/admin/v1/api_token_service_pb";
import { CapabilityService } from "@/gen/paladin/admin/v1/capability_service_pb";
import { TenantBudgetService } from "@/gen/paladin/admin/v1/tenant_budget_service_pb";
import { BillingService } from "@/gen/paladin/admin/v1/billing_service_pb";
import { MCPInspectService } from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";

// data plane services
import { ObjectService } from "@/gen/paladin/data/v1/object_service_pb";
import { ObjectTagService } from "@/gen/paladin/data/v1/object_tag_service_pb";
import { BatchService } from "@/gen/paladin/data/v1/batch_service_pb";
import { MultipartUploadService } from "@/gen/paladin/data/v1/multipart_service_pb";
import { PresignService } from "@/gen/paladin/data/v1/presign_service_pb";
import { OperationService as DataOperationService } from "@/gen/paladin/data/v1/operation_service_pb";

// iam plane services
import { AuthService } from "@/gen/paladin/iam/v1/auth_service_pb";
import { UserService } from "@/gen/paladin/iam/v1/user_service_pb";
import { UserSettingsService } from "@/gen/paladin/iam/v1/user_settings_service_pb";
import { SystemService } from "@/gen/paladin/iam/v1/system_service_pb";

/**
 * BFF that proxies browser RPCs to the three backend planes.
 *
 * Routing: the browser hits `${RPC_API_PREFIX}${RPC_PLANE_PREFIXES[plane]}/...`
 * (e.g. `/api/rpc/data/paladin.data.v1.ObjectService/ListObjects`). We strip the
 * plane prefix, look up the service in that plane's router, and forward to
 * the matching in-cluster backend with the browser's Authorization header
 * passed through.
 *
 * Per-plane backend URLs are env-configurable so the chart can map
 * `PALADIN_<PLANE>_URL` → `http://paladin-core:{8080,8085,8090}`.
 */

const planeBackendUrls: Record<Plane, string> = {
  data: process.env.PALADIN_DATA_URL || "http://paladin-core:8080",
  iam: process.env.PALADIN_IAM_URL || "http://paladin-core:8085",
  admin: process.env.PALADIN_ADMIN_URL || "http://paladin-core:8090",
};

const planeServices: Record<Plane, DescService[]> = {
  admin: [
    ObjectKeyService,
    BucketService,
    TenantService,
    PolicyService,
    CELService,
    BackendService,
    QuotaService,
    AuditLogService,
    EventSubscriptionService,
    AdminOperationService,
    AdminSystemService,
    APITokenService,
    CapabilityService,
    TenantBudgetService,
    BillingService,
    MCPInspectService,
  ],
  data: [
    ObjectService,
    ObjectTagService,
    BatchService,
    MultipartUploadService,
    PresignService,
    DataOperationService,
  ],
  iam: [AuthService, UserService, UserSettingsService, SystemService],
};

// ContextKey must be constructed via createContextKey — a bare Symbol has
// no `id`/`defaultValue` fields, so two such keys collide on `this[undefined]`
// inside ContextValues and the second .set() silently overwrites the first.
const authKey = createContextKey<string | null>(null, { description: "auth" });

/**
 * Build one router + internal transport per plane. Done lazily (per cold
 * start) so dev hot-reload picks up env changes without restart hassles.
 */
function buildPlaneRouter(plane: Plane) {
  const internalTransport = createGrpcWebTransport({
    baseUrl: planeBackendUrls[plane],
    interceptors: [
      (next) => async (req) => {
        try {
          return await next(req);
        } catch (err) {
          if (err instanceof ConnectError && err.metadata) {
            err.metadata.delete("content-type");
            err.metadata.delete("content-length");
            err.metadata.delete("transfer-encoding");
          }
          throw err;
        }
      },
    ],
  });

  const router = createConnectRouter();

  for (const service of planeServices[plane]) {
    const internalClient = createClient(service, internalTransport);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const impl = {} as any;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    for (const method of service.methods as any) {
      const name = method.localName;
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      impl[name] = async (req: any, ctx: any) => {
        const auth = ctx.values.get(authKey);
        // Tenant comes from the JWT `tenant` claim — backend resolves it
        // server-side. No X-Tenant-ID forwarding here.
        //
        // Forward the browser-minted Idempotency-Key end-to-end. The BFF
        // re-issues the call to the backend on a fresh internal transport, so
        // without this the header the browser's idempotencyInterceptor set is
        // dropped and the backend's RequireOnCreate gate rejects every
        // Create*/Issue* RPC ("missing Idempotency-Key header"). Preserve the
        // SAME key (don't mint a new one per hop) so a browser retry collapses
        // onto the original request server-side.
        const idempotencyKey = ctx.requestHeader?.get("Idempotency-Key");
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        return await (internalClient as any)[name](req, {
          headers: {
            ...(auth ? { Authorization: auth } : {}),
            ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {}),
          },
        });
      };
    }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    router.service(service as any, impl as any);
  }

  return router;
}

const planeRouters: Record<Plane, ReturnType<typeof buildPlaneRouter>> = {
  admin: buildPlaneRouter("admin"),
  data: buildPlaneRouter("data"),
  iam: buildPlaneRouter("iam"),
};

export const dynamic = "force-dynamic";

function planeFromPath(
  pathname: string,
): { plane: Plane; rest: string } | null {
  if (!pathname.startsWith(RPC_API_PREFIX)) return null;
  const afterPrefix = pathname.slice(RPC_API_PREFIX.length);
  for (const [plane, sub] of Object.entries(RPC_PLANE_PREFIXES) as [
    Plane,
    string,
  ][]) {
    if (afterPrefix.startsWith(sub + "/")) {
      return { plane, rest: afterPrefix.slice(sub.length) };
    }
  }
  return null;
}

async function handler(req: Request) {
  const url = new URL(req.url);
  const route = planeFromPath(url.pathname);

  if (!route) {
    console.warn(`[RPC Bridge] No plane prefix in ${url.pathname}`);
    return new Response(null, { status: 404 });
  }

  const { plane, rest } = route;
  const router = planeRouters[plane];
  const uHandler = router.handlers.find((h) => h.requestPath === rest);

  if (!uHandler) {
    console.warn(`[RPC Bridge] No handler in plane=${plane} for ${rest}`);
    return new Response(null, { status: 404 });
  }

  const authHeader = req.headers.get("Authorization");

  // Audience gate: refuse to forward a token whose `aud` doesn't match the
  // plane it's aimed at. The BFF is otherwise an open proxy to internal,
  // cluster-only planes; without this a caller could tunnel a data-audience
  // token to the admin plane. The backend re-verifies the signature and
  // audience — this is the BFF's defense-in-depth half (and a clean 403
  // instead of a confusing downstream error). Absent token → anonymous
  // flow (e.g. iam AuthService.Login), let the backend decide.
  if (!tokenMatchesPlane(authHeader, plane)) {
    console.warn(`[RPC Bridge] token audience mismatch for plane=${plane}`);
    return new Response(
      JSON.stringify({
        code: "permission_denied",
        message: "token audience does not match the requested plane",
      }),
      { status: 403, headers: { "Content-Type": "application/json" } },
    );
  }

  const contextValues = createContextValues().set(authKey, authHeader);

  const incomingBody = req.body
    ? (req.body as unknown as AsyncIterable<Uint8Array>)
    : (async function* () {})();

  try {
    const uRes = await uHandler({
      httpVersion: "2.0",
      method: req.method,
      url: req.url,
      header: req.headers,
      contextValues,
      body: incomingBody,
      signal: req.signal,
    });

    const responseHeaders = new Headers(uRes.header);
    if (uRes.trailer) {
      uRes.trailer.forEach((value, key) => {
        responseHeaders.append(key, value);
      });
    }

    let bodyStream: BodyInit | null = null;
    if (uRes.body) {
      bodyStream = new ReadableStream({
        async start(controller) {
          try {
            for await (const chunk of uRes.body as AsyncIterable<Uint8Array>) {
              controller.enqueue(chunk);
            }
            controller.close();
          } catch (e) {
            controller.error(e);
          }
        },
      });
    }

    return new Response(bodyStream, {
      status: uRes.status,
      headers: responseHeaders,
    });
  } catch (e: unknown) {
    console.error(
      `[RPC Bridge] Error handling ${req.method} plane=${plane} ${rest}:`,
      e,
    );
    return new Response(
      JSON.stringify({ code: "internal", message: "Internal Server Error" }),
      { status: 500, headers: { "Content-Type": "application/json" } },
    );
  }
}

export { handler as GET, handler as POST };
