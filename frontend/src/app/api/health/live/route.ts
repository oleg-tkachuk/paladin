import { NextResponse } from "next/server";

// Liveness probe — process-alive only, NO backend/DB calls. The Next
// process answering at all means it's live; kubelet should restart it
// only when the process itself is wedged. (Readiness, which legitimately
// depends on upstreams, lives at /api/health/all.) Pointing the liveness
// probe at a dependency-checking endpoint causes restart storms: a
// backend/DB outage trips the probe, kubelet kills the pod, it restarts
// into the same broken environment and trips again — so even cached
// static pages stop being served during an incident.
export const dynamic = "force-dynamic";

export function GET() {
  return NextResponse.json({ status: "ok" }, { status: 200 });
}
