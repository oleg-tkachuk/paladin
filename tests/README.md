# PALADIN test suite

End-to-end + fuzz tests that exercise the **live Connect API** against a
running backend. They do not stub anything — every call lands in the real
postgres + S3 stack via the real handlers. The suite is the contract for
"the API works as intended"; if it goes red, the deployment is broken.

## Layout

```
tests/api/
├── lib/
│   ├── connect.sh        helpers — `rpc <Service>/<Method> <body>` over Connect/JSON
│   └── gen-jwt.sh        mints a HS256 JWT signed with the dev secret
├── e2e/
│   ├── run-e2e.sh        orchestrator — provisions tenant/bucket/object_key,
│   │                      runs every .hurl file, then cleans up in reverse
│   ├── health.hurl       /livez /readyz /startupz
│   ├── system.hurl       SystemService (GetVersion / GetHealth / GetConfig)
│   ├── tenant.hurl       TenantService CRUD
│   ├── bucket.hurl       BucketService CRUD
│   ├── object_key.hurl   ObjectKeyService CRUD + FK-blocking-delete negative test
│   ├── object_lifecycle.hurl
│   │                      ObjectService UploadObject → S3 PUT → CompleteObject
│   │                      → GetObject → DownloadObject (round-trip) → soft/restore/purge
│   ├── multipart.hurl    MultipartUploadService initiate → presign part → abort
│   ├── object_tag.hurl   ObjectTagService CRUD
│   └── fixtures/hello.txt
├── functional/
│   └── run-functional.sh single shell script that exercises every service in one
│                          shot using `curl`. Faster than hurl, no deps beyond
│                          `bash + curl + jq + openssl`. Use this for CI smoke.
├── fuzz/
│   └── run-fuzz.sh       negative-input batter — every RPC × ~8 invalid bodies.
│                          Asserts every response is 4xx; 5xx is a bug to fix.
├── security/             Trivy / gosec / Nuclei / ZAP harnesses (image + endpoint scans)
└── run-all.sh            top-level orchestrator — runs functional, e2e, and fuzz
```

## Auth contract

The dev backend (`configs/config.yaml`) verifies HS256 with:

| field        | value                                     |
| ------------ | ----------------------------------------- |
| `iss`        | `paladin-dev`                                 |
| `aud`        | `paladin-api`                                 |
| `secret`     | `dev-secret-change-me-32-bytes-min`       |

`gen-jwt.sh <tenant_uuid>` mints a token with `roles=["platform-admin"]`
that satisfies every RPC. Override the issuer / audience / secret with
`JWT_ISS`, `JWT_AUD`, `JWT_HMAC` if you point the suite at a different
config.

## Hierarchy under test

```
storage_backend (config-seeded)
└── bucket          BucketService.CreateBucket
    └── object_key  ObjectKeyService.CreateObjectKey  (FK: backend_id, bucket_name)
        └── object  ObjectService.UploadObject        (FK: tenant_id, object_key)
                    →  s3://<bucket>/<tenant_id>/<object_key>/<key>
```

Every flow:

1. Generates a fresh tenant UUID
2. Mints a JWT for it
3. Creates an `s3_buckets` row (real S3 CreateBucket happens server-side)
4. Creates an `object_keys` row referencing that bucket
5. Runs the actual flow under test
6. Tears everything down in reverse so reruns on the same backend work

## Quick start

```bash
# Smoke against a locally-running PALADIN (default http://127.0.0.1:8080):
tests/api/run-all.sh

# Single stage:
tests/api/functional/run-functional.sh
tests/api/e2e/run-e2e.sh
tests/api/fuzz/run-fuzz.sh

# Pointed at a non-default endpoint:
PALADIN_HOST=https://paladin.example.com tests/api/run-all.sh

# Custom backend / bucket name:
PALADIN_BACKEND_ID=secondary PALADIN_BUCKET_NAME=paladin-staging \
    tests/api/functional/run-functional.sh

# Skip the noisy stages in PR CI:
SKIP_FUZZ=1 SKIP_HURL=1 tests/api/run-all.sh
```

## Adding a new RPC test

The template is consistent; copy the closest `.hurl` file and:

1. Add the new file to `tests/api/e2e/`.
2. Add a `run_hurl <new-file>.hurl` line to `tests/api/e2e/run-e2e.sh`
   in the right phase (the bucket → object_key → object hierarchy is
   ordered).
3. If the RPC needs an extra var, pass it via `--variable name=value` in
   `run-e2e.sh`.
4. Mirror the same flow in `functional/run-functional.sh` if it belongs
   in the always-on smoke.

## Required tooling

| tool      | used by           | install                                              |
| --------- | ----------------- | ---------------------------------------------------- |
| `bash`    | everything        | shipped                                              |
| `curl`    | functional + fuzz | shipped                                              |
| `jq`      | functional + fuzz | `brew install jq` / `apt install jq`                 |
| `openssl` | gen-jwt           | shipped                                              |
| `uuidgen` | functional        | shipped                                              |
| `hurl`    | e2e               | `brew install hurl` / see <https://hurl.dev/install> |

Security scripts (`gosec`, `trivy`, `nuclei`, `zap`) self-document their
own deps via `--help`.
