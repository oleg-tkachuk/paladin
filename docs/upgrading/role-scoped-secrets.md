## Unreleased — each role reads only the Secrets it consumes

- In a cluster, a process now resolves only the `*_secret` references its own
  role uses: `serve mcp` (the bridge) reads its DPoP key and token-exchange
  client and nothing else, `migrate` the two Postgres passwords it opens,
  and so on (`backend/internal/config/roles.go`). A reference another role
  uses keeps no value in this one, and a boot line names the ones left.
- Nothing changes in what a deployment must provide, nor in RBAC: the chart
  still lets every role read every Secret it names, until the release that
  narrows it per role.
