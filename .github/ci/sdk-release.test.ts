import assert from "node:assert/strict";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { after, before, describe, test } from "node:test";

import { fakeData, fakeGitHub } from "./lib/fake-github.ts";
import { tempRepo, type TempRepo } from "./lib/temp-repo.ts";
import { NO_RELEASE, anchor, moduleInfoUrl, publishStream, releaseSummary, streamNotes, upgradeHeading, warmGoProxy } from "./sdk-release.ts";

const REPO = "o/r";

describe("streamNotes: only what the stream released", () => {
  let repo: TempRepo;
  const change = async (path: string, message: string, body?: string) => {
    repo.write(path, `${message}\n${Math.random()}`);
    await repo.commit(body === undefined ? message : `${message}\n\n${body}`);
  };
  before(async () => {
    repo = await tempRepo();
    await change("sdk/go/client.go", "feat(sdk): the first helper");
    await repo.run("tag", "sdk/go/v0.1.0");
    await change("sdk/go/client.go", "feat(sdk): a second helper");
    await change("proto/api.proto", "feat(events): a new RPC");
    await change("backend/main.go", "feat(api): a server feature");
    await change("sdk/python/client.py", "fix(sdk): a python fix");
    await change("sdk/go/README.md", "docs(sdk): explain it");
    await change("backend/go.mod", "fix(auth): take a limes fix");
    await repo.run("tag", "sdk/go/v0.2.0");
    await repo.run("tag", "v1.0.0");
  });
  after(() => repo.cleanup());

  test("the first tag", async () => {
    const notes = await streamNotes("sdk/go/v0.1.0", repo.dir, REPO);
    assert.equal(notes.title, "Go SDK 0.1.0");
    assert.match(notes.body, /first release/);
  });

  test("the stream's own commits since its previous tag", async () => {
    const { title, body } = await streamNotes("sdk/go/v0.2.0", repo.dir, REPO);
    assert.equal(title, "Go SDK 0.2.0");
    for (const want of ["feat(sdk): a second helper", "feat(events): a new RPC", "compare/sdk/go/v0.1.0...sdk/go/v0.2.0"]) {
      assert.ok(body.includes(want), want);
    }
    for (const unwanted of ["a server feature", "explain it", "a limes fix", "the first helper", "a python fix", "Behaviour changes"]) {
      assert.ok(!body.includes(unwanted), unwanted);
    }
  });

  test("a breaking release lists its breaks first, linked to the guide", async () => {
    repo.write("docs/upgrading.md", "# Upgrading\n\n## v0.3.0 — a product release\n\nText.\n\n## v1.0.0, sdk/go/v0.3.0 — the SDK's calls change\n\nText.\n");
    await change("sdk/go/client.go", "feat(sdk)!: rename a helper");
    await change("sdk/go/other.go", "fix(sdk): stop retrying a call", "BREAKING CHANGE: the call is no longer retried.");
    await change("sdk/go/client.go", "feat(sdk): an ordinary helper");
    await repo.run("tag", "sdk/go/v0.3.0");
    const { body } = await streamNotes("sdk/go/v0.3.0", repo.dir, REPO);
    const [behaviour = "", features = ""] = body.split("## Features");
    for (const want of ["## Behaviour changes", "feat(sdk)!: rename a helper", "fix(sdk): stop retrying a call", "blob/main/docs/upgrading.md#v100-sdkgov030--the-sdks-calls-change"]) {
      assert.ok(behaviour.includes(want), want);
    }
    assert.ok(features.includes("an ordinary helper"));
    assert.ok(!features.includes("rename a helper"), "a breaking commit is listed twice");
  });

  test("a product tag has no stream notes", async () => {
    await assert.rejects(streamNotes("v1.0.0", repo.dir, REPO), /is not an SDK tag/);
  });
});

test("a heading names a tag whole, not a tag it starts", () => {
  const guide = "## v1.0.0, sdk/go/v0.30.0 — later\n## sdk/go/v0.3.0-rc — a candidate\n## sdk/go/v0.3.0 — this one\n";
  assert.equal(upgradeHeading(guide, "sdk/go/v0.3.0"), "sdk/go/v0.3.0 — this one");
  assert.equal(upgradeHeading(guide, "sdk/go/v0.4.0"), undefined);
  assert.equal(anchor("v1.0.0, sdk/go/v0.3.0 — the SDK's calls change"), "v100-sdkgov030--the-sdks-calls-change");
});

describe("the Go proxy", () => {
  test("each module tag asks for its own module and version", () => {
    assert.equal(moduleInfoUrl("sdk/go/v0.16.0", REPO, "http://p"), "http://p/github.com/o/r/sdk/go/@v/v0.16.0.info");
    assert.equal(moduleInfoUrl("mod/v0.3.0", REPO, "http://p"), "http://p/github.com/o/r/mod/@v/v0.3.0.info");
    for (const bad of ["v6.1.0", "sdk/go/latest", "api/v0.12"]) assert.throws(() => moduleInfoUrl(bad, REPO), /is not a module tag/, bad);
    assert.throws(() => moduleInfoUrl("Sdk/v1.0.0", REPO), /upper-case/);
  });

  // A stand-in proxy answering 404 for the first misses, then the version.
  let server: Server;
  let base = "";
  let misses = 0;
  const asked: string[] = [];
  before(async () => {
    server = createServer((req, res) => {
      asked.push(req.url ?? "");
      if (misses > 0) {
        misses--;
        res.writeHead(404).end();
      } else {
        res.writeHead(200).end('{"Version":"v0.16.0"}');
      }
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  });
  after(() => server.close());

  test("asked again until it answers", async () => {
    misses = 2;
    assert.match(await warmGoProxy(`${base}/x.info`, 0), /v0.16.0/);
    misses = 99;
    await assert.rejects(warmGoProxy(`${base}/x.info`, 0), /404/);
    misses = 0;
  });

  test("the release stands when the proxy does not answer", async () => {
    const data = fakeData();
    const repo = await tempRepo();
    try {
      repo.write("sdk/go/a.go", "a");
      await repo.commit("feat(sdk): a");
      await repo.run("tag", "sdk/go/v0.1.0");
      asked.length = 0;
      misses = 99;
      await publishStream(fakeGitHub(data), "sdk/go/v0.1.0", repo.dir, REPO, base, 0);
      misses = 0;
      assert.deepEqual(
        data.published.map((r) => [r.tag, r.title, r.latest]),
        [["sdk/go/v0.1.0", "Go SDK 0.1.0", false]],
      );
      assert.ok(asked.length > 0 && asked.every((u) => u === "/github.com/o/r/sdk/go/@v/v0.1.0.info"));
    } finally {
      repo.cleanup();
    }
  });

  // A Python SDK tag is no Go module version: its release asks the proxy
  // nothing, and its notes are the Python SDK's commits alone.
  test("a Python SDK release is its own, and no Go module", async () => {
    const data = fakeData();
    const repo = await tempRepo();
    try {
      repo.write("sdk/python/a.py", "a");
      await repo.commit("feat(sdk): a");
      await repo.run("tag", "sdk/python/v0.1.0");
      repo.write("sdk/go/a.go", "a");
      await repo.commit("feat(sdk): a Go helper");
      repo.write("sdk/python/a.py", "b");
      await repo.commit("fix(sdk): a Python fix");
      await repo.run("tag", "sdk/python/v0.1.1");
      asked.length = 0;
      await publishStream(fakeGitHub(data), "sdk/python/v0.1.1", repo.dir, REPO, base, 0);
      assert.deepEqual(
        data.published.map((r) => [r.tag, r.title, r.latest]),
        [["sdk/python/v0.1.1", "Python SDK 0.1.1", false]],
      );
      assert.deepEqual(asked, []);
      const { body } = await streamNotes("sdk/python/v0.1.1", repo.dir, REPO);
      assert.ok(body.includes("a Python fix") && !body.includes("a Go helper"), body);
    } finally {
      repo.cleanup();
    }
  });
});

// The summary names every SDK, released or not, so a run that released one
// says so of the other too.
test("releaseSummary has a row per SDK", () => {
  const summary = releaseSummary(["sdk/python/v0.103.0", "v39.3.0"], REPO);
  assert.ok(summary.includes("| Go SDK | no release |"), summary);
  assert.ok(summary.includes("| Python SDK | [sdk/python/v0.103.0](https://github.com/o/r/releases/tag/sdk/python/v0.103.0) |"), summary);
  assert.equal(summary.includes("v39.3.0"), false, "the product's tag is not an SDK's");
  const none = releaseSummary([], REPO);
  assert.equal(none.split(NO_RELEASE).length - 1, 2, none);
});
