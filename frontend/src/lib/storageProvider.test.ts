import { describe, expect, it } from "vitest";

import { providerFromEndpoint, providerLabel } from "./storageProvider";

describe("providerFromEndpoint", () => {
  it.each([
    ["https://s3.eu-central-1.amazonaws.com", "aws"],
    ["s3.amazonaws.com", "aws"],
    ["https://storage.googleapis.com", "gcp"],
    ["https://fra1.digitaloceanspaces.com", "digitalocean"],
    ["https://acct.r2.cloudflarestorage.com", "cloudflare"],
    ["https://s3.wasabisys.com", "wasabi"],
    ["https://s3.us-west-002.backblazeb2.com", "backblaze"],
    ["https://acct.blob.core.windows.net", "azure"],
    ["http://garage:3900", "garage"],
    ["http://seaweedfs-s3.storage.svc:8333", "seaweedfs"],
    ["minio:9000", "minio"],
  ])("%s → %s", (endpoint, slug) => {
    expect(providerFromEndpoint(endpoint)).toBe(slug);
  });

  // The substring match these replace said "aws" for every one of them.
  it.each([
    "https://amazonaws.com.attacker.test",
    "https://attacker.test/amazonaws.com",
    "https://attacker.test/?u=s3.amazonaws.com",
    "https://notamazonaws.com",
  ])("does not take %s for a hosted provider", (endpoint) => {
    expect(providerFromEndpoint(endpoint)).toBe("");
  });

  it("reads only the host for self-hosted names", () => {
    expect(providerFromEndpoint("http://10.0.0.5:9000/minio")).toBe("");
  });

  it.each(["", "   ", "http://"])("gives up on %j", (endpoint) => {
    expect(providerFromEndpoint(endpoint)).toBe("");
  });
});

describe("providerLabel", () => {
  it("prefers the explicit provider", () => {
    expect(
      providerLabel({ provider: "ceph", endpoint: "http://minio:9000" }),
    ).toEqual({ label: "Ceph", derived: false });
  });

  it("marks a guess from the endpoint as derived", () => {
    expect(providerLabel({ provider: "", endpoint: "minio:9000" })).toEqual({
      label: "MinIO",
      derived: true,
    });
  });

  it("renders an unknown slug verbatim and nothing as a dash", () => {
    expect(providerLabel({ provider: "acme-s3", endpoint: "" }).label).toBe(
      "acme-s3",
    );
    expect(providerLabel({ provider: "", endpoint: "" })).toEqual({
      label: "—",
      derived: false,
    });
  });
});
