// NEVER true in prod. e2e-only fixture credentials.
//
// Mirrors the local-overlay convention from gitops/.../overlays/
// local/values/paladin/paladin-core.yaml (the `local-dev-admin-
// pw-*` pattern). Constitution Principle V requires the
// NEVER-in-prod marker above this comment to remain in place — these
// credentials are baked into the test stack's bootstrap step, so
// reusing them anywhere a real user can reach is a real
// privilege-escalation hazard.
//
// The seeded admin is provisioned by the `bootstrap` one-shot
// container in tests/e2e/docker-compose.test.yaml. Every test logs
// in through the real /login form using these credentials,
// exercising the full JWT + AuthGate chain (FR-003, FR-004).
export const SEEDED_ADMIN = {
  subject: "e2e-admin@local",
  password: "e2e-not-a-secret-2026",
} as const;
