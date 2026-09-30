import { describe, expect, it } from "vitest";

import { routeNeedsAdminPlane } from "./adminPlaneRoutes";

describe("routeNeedsAdminPlane", () => {
  it.each(["/", "/health", "/profile"])("%s works without it", (p) => {
    expect(routeNeedsAdminPlane(p)).toBe(false);
  });

  it.each([
    "/tenants",
    "/tenants/acme/buckets",
    "/users",
    "/upload",
    "/healthcheck",
    "/profile-other",
  ])("%s needs it", (p) => {
    expect(routeNeedsAdminPlane(p)).toBe(true);
  });
});
