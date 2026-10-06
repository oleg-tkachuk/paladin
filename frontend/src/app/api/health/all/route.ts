import { NextRequest, NextResponse } from "next/server";

import { requireSession } from "@/lib/auth/session";

import { resolveHealthToken } from "./token";

// Health aggregator — fans out to /system/health.json on each backend
// role and returns a merged response the /health UI page renders. Each
// upstream is independent: a single role being unreachable degrades
// only that pane, not the whole page. Parallel fetches with a bounded
// timeout keep the page interactive even when one role is wedged.
//
// Why a server-side aggregator rather than four parallel client fetches:
//   1. Cluster-internal Service DNS (paladin-core-worker etc.)
//      isn't resolvable from a browser.
//   2. The browser would need separate auth/CORS handling per role.
//   3. One round trip from the browser keeps the page snappy on a
//      flaky cluster network.
//
// Endpoint shape — same Snapshot contract as backend/internal/health:
//   { roles: Snapshot[] }
// where each Snapshot carries role, status, components[], checked_at.
// A role that fails to respond shows up with status="unhealthy" and a
// synthetic component carrying the network error message.

const ROLES: { name: string; envKey: string; defaultUrl: string }[] = [
  // The api binary opens both data + iam Connect listeners in one
  // process; either port serves the same /system/health.json. We pick
  // iam (8085) because that's what the rest of the BFF already uses
  // for SystemService — no new firewall surface.
  {
    name: "api",
    envKey: "PALADIN_IAM_URL",
    defaultUrl: "http://paladin-core-api:8085",
  },
  {
    name: "admin",
    envKey: "PALADIN_ADMIN_URL",
    defaultUrl: "http://paladin-core-admin:8090",
  },
  {
    name: "worker",
    envKey: "PALADIN_WORKER_URL",
    // 8099 is the Service port (containerPort is 8090). See chart's
    // backend values.yaml `deployments.worker.service.ports`.
    defaultUrl: "http://paladin-core-worker:8099",
  },
  {
    name: "mcp",
    envKey: "PALADIN_MCP_URL",
    defaultUrl: "http://paladin-core-mcp:8095",
  },
  // Dispatcher pod — durable webhook fan-out (event_deliveries outbox,
  // `001_initial_schema.sql`). Same ops shape as worker; the BFF aggregator just
  // needs a /system/health.json endpoint.
  {
    name: "dispatcher",
    envKey: "PALADIN_DISPATCHER_URL",
    defaultUrl: "http://paladin-core-dispatcher:8099",
  },
  // Ingest pod — storage-event consumer (SeaweedFS / MinIO bucket
  // notifications → outbox state-promote pipeline). Container port
  // is 8100 (cfg.Ingest.Webhook.Addr) regardless of driver — the
  // webhook driver binds it for receiver routes; nats / rabbitmq
  // drivers bind the same addr for ops-only via
  // serve_ingest.go::runIngestOpsServer. `subscriber` subsystem
  // check on the snapshot reports broker connectivity.
  {
    name: "ingest",
    envKey: "PALADIN_INGEST_URL",
    defaultUrl: "http://paladin-core-ingest:8100",
  },
];

// Per-role timeout. Each role's own snapshot is bounded by its probe
// budget; this adds a hop, so 5s lets a slow-but-healthy role answer
// without making the page hang on a wedged one. Not a kubelet probe
// target: the console's probes use /api/health/live.
const TIMEOUT_MS = 5000;

type Snapshot = {
  role: string;
  status: "healthy" | "degraded" | "unhealthy";
  components: Component[];
  checked_at: string;
};

type Component = {
  name: string;
  status: "healthy" | "degraded" | "unhealthy";
  message?: string;
  latency_ms: number;
  category: string;
  critical: boolean;
};

// Shared secret gating /system/health.json on the backends. When set
// (prod), the backends reject an unauthenticated snapshot fetch with 401;
// we forward it as X-Health-Token. Unset (dev) → backends leave the
// endpoint open and the header is simply absent. See ./token.
const HEALTH_TOKEN = resolveHealthToken(process.env);

async function fetchSnapshot(role: string, baseUrl: string): Promise<Snapshot> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), TIMEOUT_MS);
  try {
    const res = await fetch(`${baseUrl}/system/health.json`, {
      signal: ctrl.signal,
      cache: "no-store",
      headers: HEALTH_TOKEN ? { "X-Health-Token": HEALTH_TOKEN } : undefined,
    });
    // 200 = healthy/degraded; 503 = draining (still has body) — both carry
    // the per-component snapshot. Network/parse errors fall through to the
    // synthetic-failure path below.
    const body = (await res.json()) as Snapshot;
    return body;
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    return {
      role,
      status: "unhealthy",
      components: [
        {
          name: "reachability",
          status: "unhealthy",
          message: `unreachable: ${msg}`,
          latency_ms: 0,
          category: "upstream",
          critical: true,
        },
      ],
      checked_at: new Date().toISOString(),
    };
  } finally {
    clearTimeout(timer);
  }
}

// Which roles to poll: PALADIN_HEALTH_ROLES, comma-separated. A role the
// deployment does not run would otherwise report as unreachable on every
// check. Unset means every role in the table above.
export const HEALTH_ROLES_ENV = "PALADIN_HEALTH_ROLES";

function polledRoles(): typeof ROLES {
  const raw = process.env[HEALTH_ROLES_ENV];
  if (raw === undefined) return ROLES;
  const wanted = new Set(
    raw
      .split(",")
      .map((r) => r.trim())
      .filter(Boolean),
  );
  return ROLES.filter((r) => wanted.has(r.name));
}

export async function GET(_req: NextRequest) {
  // proxy.ts leaves /api/health public for the kubelet's /api/health/live,
  // and this route attaches the server-held snapshot token itself — so it
  // must check the session, or anyone reaching the console reads every role's
  // snapshot.
  const refused = await requireSession();
  if (refused) {
    return refused;
  }
  const snapshots = await Promise.all(
    polledRoles().map((r) =>
      fetchSnapshot(r.name, process.env[r.envKey] || r.defaultUrl),
    ),
  );
  return NextResponse.json({ roles: snapshots });
}
