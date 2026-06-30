import { it } from "vitest";
import { seedTenant } from "../tests/e2e/fixtures/seed";

it("repro seed createTenant", async () => {
  const t = await seedTenant();
  process.stdout.write(`SEED_OK tenantId=${t.tenantId} slug=${t.slug}\n`);
}, 30_000);
