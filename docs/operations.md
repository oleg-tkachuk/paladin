# Operations

The `paladin` is designed to be deployed as a containerized microservice, primarily targeting Kubernetes environments.

## 1. Build System & Tooling

The project uses [Task](https://taskfile.dev/) (`Taskfile.yaml`) as its primary task runner.

**Common Commands:**

- `task build`: Automatically bumps the patch version, writes build metadata, and triggers a Docker image build.
- `task test`: Runs Go unit tests.
- `task test:hurl`: Runs Hurl integration tests.
- `task generate`: Generates Go code from Protobuf definitions, OpenAPI specs, and mock files.
- `task up`: Stands up a local Docker Compose stack (`deploy/docker-compose.yaml`).

## 2. Containerization (Docker)

The application is packaged using a multi-stage `Dockerfile` located at `deploy/Dockerfile`.

**Stages:**

1. **tools**: Fetches exact pinned versions of code generators (`protoc-gen-go`, `wire`, `oapi-codegen`).
2. **builder**: Copies source code, downloads Go modules, runs `go generate`, and compiles the static binary. It aggressively strips debug symbols and injects build metadata (`version`, `commit`, `buildTime`) via `-ldflags`.
3. **runtime**: Uses `gcr.io/distroless/static:nonroot` as the base image for maximum security and minimal attack surface.

**Container Artifacts:**

- The Go binary resides at `/app/bin/paladin`.
- Configuration templates are loaded into `/app/configs`.
- PostgreSQL migrations are bundled into `/app/migrations` allowing the application to optionally apply migrations on startup (or via init containers).

**Runtime Configuration:**

- Processes run as user `nonroot:nonroot`.
- Default exposed ports: `8080` (HTTP), `9090` (gRPC).

## 3. Deployment Topology (Kubernetes)

- **Namespace**: Typically deployed in the `paladin` namespace.
- **Scaling**: Horizontally scalable. The service is stateless (state is wholly managed in Postgres/S3).
- **Probes**: Kubernetes uses `/health/livez` and `/health/readyz` to manage pod routing.
- **Secrets**: Bound to Kubernetes Secrets and resolved at startup using the internal configuration resolver.

## 4. Database Migrations

Migrations are written using `goose` and are strictly managed in the `migrations/` directory.

- The migrations define the DDL schemas.
- When new schema changes are added, they must be numbered sequentially (e.g., `011_new_feature.sql`).
- It traverses these files during CI/CD to ensure identical state across environments.
