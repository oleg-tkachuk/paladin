package testimage

// Postgres is the server every testcontainers suite runs: the major the compose
// stacks and the cluster run, so a test passes against the Postgres Paladin
// is deployed on. scripts/postgres-version.test.sh holds them together, and
// Renovate updates them as one.
const Postgres = "postgres:18.6-alpine"
