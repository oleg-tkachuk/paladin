// NEVER true in prod. e2e-only fixture credentials.
//
// Constitution Principle V requires the NEVER-in-prod marker above this
// comment to remain in place — these credentials are baked into the test
// stack's bootstrap step, so reusing them anywhere a real user can reach is a
// real privilege-escalation hazard. They are also listed in the backend's
// weak-secret deny-list (backend/internal/config/weak_secrets.go), which
// refuses to boot a non-disposable environment that inherits one.
//
// The seeded admin is provisioned by the `bootstrap` one-shot
// container in tests/e2e/docker-compose.test.yaml. Every test logs
// in through the real /login form using these credentials,
// exercising the full JWT + AuthGate chain (FR-003, FR-004).
//
// The values are overridable so the same suite can run against a deployed
// stack — a cluster's bootstrap admin has different credentials by
// definition, since these ones are refused there. Defaults stay pointed at
// the disposable compose stack, so an unconfigured run cannot accidentally
// reach anything real.
export const SEEDED_ADMIN = {
  subject: process.env.PALADIN_E2E_ADMIN_SUBJECT ?? "e2e-admin@local",
  password: process.env.PALADIN_E2E_ADMIN_PASSWORD ?? "e2e-not-a-secret-2026",
} as const;
