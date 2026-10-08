// Package pgtest names the Postgres the test suites start in a container.
package pgtest

// Image is the server every testcontainers suite runs: the major the compose
// stacks and the cluster run, so a test passes against the Postgres Paladin
// is deployed on. scripts/postgres-version.test.sh holds them together, and
// Renovate updates them as one.
const Image = "postgres:18.6-alpine"
