import { NextRequest, NextResponse } from "next/server";

// Health aggregator — fans out to /system/health.json on each backend
// role and returns a merged response the /health UI page renders. Each
// upstream is independent: a single role being unreachable degrades
// only that pane, not the whole page. Parallel fetches with a bounded
// timeout keep the page interactive even when one role is wedged.
//
// Why a server-side aggregator rather than four parallel client fetches:
//   1. Cluster-internal Service DNS (paladin-worker etc.)
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
    defaultUrl: "http://paladin-api:8085",
  },
  {
    name: "admin",
    envKey: "PALADIN_ADMIN_URL",
    defaultUrl: "http://paladin-admin:8090",
  },
  {
    name: "worker",
    envKey: "PALADIN_WORKER_URL",
    // 8099 is the Service port (containerPort is 8090). See chart's
    // backend values.yaml `deployments.worker.service.ports`.
    defaultUrl: "http://paladin-worker:8099",
  },
  {
    name: "mcp",
    envKey: "PALADIN_MCP_URL",
    defaultUrl: "http://paladin-mcp:8095",
  },
  // Dispatcher pod — durable webhook fan-out (event_deliveries outbox,
  // migration 028). Same ops shape as worker; the BFF aggregator just
  // needs a /system/health.json endpoint.
  {
    name: "dispatcher",
    envKey: "PALADIN_DISPATCHER_URL",
    defaultUrl: "http://paladin-dispatcher:8099",
  },
];

// Per-role timeout. The kubelet probe timeout is 3s; fetching the
// snapshot here adds a hop, so 5s gives a healthy probe time to land
// without making the page hang on a wedged role.
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

async function fetchSnapshot(role: string, baseUrl: string): Promise<Snapshot> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), TIMEOUT_MS);
  try {
    const res = await fetch(`${baseUrl}/system/health.json`, {
      signal: ctrl.signal,
      cache: "no-store",
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

export async function GET(_req: NextRequest) {
  const snapshots = await Promise.all(
    ROLES.map((r) =>
      fetchSnapshot(r.name, process.env[r.envKey] || r.defaultUrl),
    ),
  );
  return NextResponse.json({ roles: snapshots });
}
