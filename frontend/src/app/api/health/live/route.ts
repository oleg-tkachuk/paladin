import { NextResponse } from "next/server";

// Liveness and readiness probe — process-alive only, NO backend/DB calls.
// The Next process answering at all means it can serve; kubelet should
// restart it only when the process itself is wedged. Pointing a probe at a
// dependency-checking endpoint causes restart storms (liveness) or takes the
// console away exactly when an operator needs it to report the outage
// (readiness). /api/health/all is the /health page's data, not a probe.
export const dynamic = "force-dynamic";

export function GET() {
  return NextResponse.json({ status: "ok" }, { status: 200 });
}
