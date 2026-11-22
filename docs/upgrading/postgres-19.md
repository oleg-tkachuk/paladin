## Unreleased — the compose stack and the test suites run Postgres 19

`backend/deploy/docker-compose.yaml`, the e2e stack and the testcontainers
suites move from Postgres 18.7 to 19.0. A major cannot read the previous
major's data directory; the image keeps its data under
`/var/lib/postgresql/19/docker`, so an existing `paladin_pg_data` volume starts
empty under 19 with the 18 data left beside it. The local stack's data is
disposable, so drop the volume and let the stack recreate it:

```
docker compose -f backend/deploy/docker-compose.yaml down -v
```

A deployment on its own Postgres needs a major upgrade (`pg_upgrade`, or a
CloudNativePG major upgrade) before it runs against 19; Paladin's migrations
run unchanged on both majors.
