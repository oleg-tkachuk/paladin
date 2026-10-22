import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, test } from "node:test";

import { fakeData, fakeGitHub } from "./lib/fake-github.ts";
import { git } from "./lib/git.ts";
import { streamOf, streams } from "./lib/release-config.ts";
import { tempRepo } from "./lib/temp-repo.ts";
import { BRANCH_PREFIX, UPGRADING, nameGuide, nameSections, parseSection, proposalTitle, propose, splitTags, tagsFor, unreleasedHeading } from "./upgrade-sections.ts";

// A section named with the wrong tag sends the release notes' migration link
// to the wrong place, or to none: the SDK's Python 3.13 section was cut in the
// same run as product v26.1.1, and belongs to the SDK alone.
const all = streams();
const PRODUCT_TAG = "v27.0.0";
const SDK_TAG = "sdk/go/v0.60.0";
const PYTHON_TAG = "sdk/python/v0.60.0";
const BOTH = [SDK_TAG, PRODUCT_TAG];
const EVERY = [PYTHON_TAG, SDK_TAG, PRODUCT_TAG];
const BACKEND = "backend/internal/api/handler.go";
const GO_SDK = "sdk/go/paladin/client.go";
const PYTHON_SDK = "sdk/python/pyproject.toml";

describe("tagsFor", () => {
  const cases: [string, string[], string[]][] = [
    ["the Python SDK and its notes, in a run without its tag", [PYTHON_SDK, UPGRADING], []],
    ["the backend and its notes", [BACKEND, UPGRADING], [PRODUCT_TAG]],
    ["the Go SDK, which the product compiles in", [GO_SDK, UPGRADING], [PRODUCT_TAG, SDK_TAG]],
    ["the notes alone read as the product's", [UPGRADING], [PRODUCT_TAG]],
  ];
  for (const [name, files, want] of cases) test(name, () => assert.deepEqual(tagsFor(files, BOTH, all), want));
  test("the Python SDK is its own stream's", () => assert.deepEqual(tagsFor([PYTHON_SDK, UPGRADING], EVERY, all), [PYTHON_TAG]));
  test("the contract is every stream's, product first", () =>
    assert.deepEqual(tagsFor(["proto/paladin/x.proto", UPGRADING], EVERY, all), [PRODUCT_TAG, SDK_TAG, PYTHON_TAG]));
  test("a run that cut no SDK tag", () => assert.deepEqual(tagsFor([PYTHON_SDK, UPGRADING], [PRODUCT_TAG], all), []));
  test("not a release tag", () => assert.deepEqual(tagsFor([BACKEND], ["api/v1.2.0"], all), []));
  test("each tag's stream", () => {
    assert.equal(streamOf(PRODUCT_TAG, all), "product");
    assert.equal(streamOf(SDK_TAG, all), "go-sdk");
    assert.equal(streamOf(PYTHON_TAG, all), "python-sdk");
    assert.equal(streamOf("sdk/go/vnext", all), undefined);
  });
});

describe("parseSection", () => {
  const cases: [string, string, ReturnType<typeof parseSection>][] = [
    ["an Unreleased section", "## Unreleased — a change", { tags: [], title: "a change" }],
    ["a section named by two streams", `## ${PRODUCT_TAG}, ${SDK_TAG} — a change — in two parts`, { tags: [PRODUCT_TAG, SDK_TAG], title: "a change — in two parts" }],
    ["the guide's own heading", "## Changing the API contract", undefined],
    ["a heading whose name is no release tag", "## Notes — a change", undefined],
    ["not a section heading", "### Unreleased — a change", undefined],
  ];
  for (const [name, line, want] of cases) test(name, () => assert.deepEqual(parseSection(line, all), want));
  test("a section is found by the heading it was written with", () =>
    assert.equal(unreleasedHeading({ tags: [SDK_TAG], title: "a change" }), "## Unreleased — a change"));
});

test("nameSections names this run's sections and leaves the rest", () => {
  const guide = ["# Upgrading", "", "## Unreleased — the SDK drops a call", "", "## Unreleased — the API and the SDK change together", "", "## Unreleased — not this run's", "", "## v26.0.0 — already named", ""].join("\n");
  const byHeading: Record<string, string[]> = {
    "## Unreleased — the SDK drops a call": [SDK_TAG],
    "## Unreleased — the API and the SDK change together": [PRODUCT_TAG, SDK_TAG],
  };
  assert.equal(
    nameSections(guide, (line) => byHeading[line] ?? [], all),
    ["# Upgrading", "", `## ${SDK_TAG} — the SDK drops a call`, "", `## ${PRODUCT_TAG}, ${SDK_TAG} — the API and the SDK change together`, "", "## Unreleased — not this run's", "", "## v26.0.0 — already named", ""].join("\n"),
  );
  const nothing = "# Upgrading\n\n## v26.0.0 — already named\n";
  assert.equal(nameSections(nothing, () => [], all), nothing);
});

test("nameSections adds a stream's tag to a section another named, in stream order", () => {
  const guide = `## ${SDK_TAG}, ${PYTHON_TAG} — the SDKs released it first\n\n## Changing the API contract\n`;
  assert.equal(
    nameSections(guide, (line) => (line.startsWith("## Changing") ? [PRODUCT_TAG] : [PRODUCT_TAG, SDK_TAG]), all),
    `## ${PRODUCT_TAG}, ${SDK_TAG}, ${PYTHON_TAG} — the SDKs released it first\n\n## Changing the API contract\n`,
  );
});

test("nameGuide reads each section's stream from the commit that wrote it", async () => {
  const repo = await tempRepo();
  try {
    repo.write(UPGRADING, "# Upgrading\n\n## Unreleased — python\n");
    repo.write(PYTHON_SDK, "x");
    await repo.commit("feat(sdk): python");
    repo.write(UPGRADING, "# Upgrading\n\n## Unreleased — server\n\n## Unreleased — python\n");
    repo.write(BACKEND, "x");
    await repo.commit("feat(api): server");
    assert.equal(await nameGuide(repo.dir, EVERY), true);
    assert.equal(await repo.run("diff", "--name-only"), `${UPGRADING}\n`);
    assert.match(await repo.run("diff"), new RegExp(`\\+## ${PRODUCT_TAG} — server[\\s\\S]*\\+## ${PYTHON_TAG.replaceAll("/", "\\/")} — python`));
    assert.equal(await nameGuide(repo.dir, EVERY), false, "a named guide is left as it is");
  } finally {
    repo.cleanup();
  }
});

// The product and the SDKs release apart. A section whose change is the
// product's and the SDK's is named by the SDK's release first; the product's
// release, later, adds its own tag rather than finding nothing Unreleased.
test("nameGuide adds the product's tag to a section the SDK released first", async () => {
  const OLD_PRODUCT_TAG = "v26.0.0";
  const OLD_SDK_TAG = "sdk/go/v0.59.0";
  const repo = await tempRepo();
  try {
    repo.write(UPGRADING, "# Upgrading\n\n## Unreleased — shipped before\n");
    repo.write(GO_SDK, "old");
    await repo.commit("feat(api): shipped before");
    await repo.run("tag", OLD_PRODUCT_TAG);
    repo.write(UPGRADING, `# Upgrading\n\n## Unreleased — both\n\n## ${OLD_SDK_TAG} — shipped before\n`);
    repo.write(GO_SDK, "new");
    await repo.commit("feat(api): both");
    await repo.run("tag", SDK_TAG);

    // The SDK's release: the product's part waits for its own.
    assert.equal(await nameGuide(repo.dir, [SDK_TAG]), true);
    await repo.commit("docs(upgrading): name the sections the SDK shipped");
    assert.match(await repo.run("show", `HEAD:${UPGRADING}`), new RegExp(`^## ${SDK_TAG.replaceAll("/", "\\/")} — both$`, "m"));
    assert.equal(await nameGuide(repo.dir, [SDK_TAG]), false, "the SDK names it once");

    // The product's release, after: its tag joins, product first. The section
    // product v26.0.0 already shipped keeps its name.
    await repo.run("tag", PRODUCT_TAG);
    assert.equal(await nameGuide(repo.dir, [PRODUCT_TAG]), true);
    const guide = await repo.run("diff");
    assert.match(guide, new RegExp(`^\\+## ${PRODUCT_TAG}, ${SDK_TAG.replaceAll("/", "\\/")} — both$`, "m"));
    assert.doesNotMatch(guide, /^\+.*shipped before/m);
    assert.equal(await nameGuide(repo.dir, [PRODUCT_TAG]), false, "the product names it once");
  } finally {
    repo.cleanup();
  }
});

describe("propose", () => {
  const tags = splitTags(" v2.0.0  sdk/go/v0.3.0 ");
  const branch = BRANCH_PREFIX + "v2.0.0-sdk/go/v0.3.0";

  test("the tags are read from one line", () => assert.deepEqual(tags, ["v2.0.0", "sdk/go/v0.3.0"]));

  test("pushes the renamed guide alone, opens its pull request once, and nothing when nothing is renamed", async () => {
    const base = mkdtempSync(join(tmpdir(), "paladin-ci-"));
    const remote = join(base, "remote.git");
    const repo = await tempRepo();
    try {
      await git(["init", "--quiet", "--bare", remote]);
      repo.write(UPGRADING, "## Unreleased — a change\n");
      await repo.commit("one");
      const data = fakeData();
      const github = fakeGitHub(data);
      const pushed = async () => git(["log", "-1", "--format=%s", branch], remote).then((s) => s.trim(), () => "");

      await propose(github, repo.dir, tags, remote);
      assert.equal(await pushed(), "", "nothing renamed pushes nothing");
      assert.equal(data.created.length, 0);

      repo.write(UPGRADING, "## v2.0.0, sdk/go/v0.3.0 — a change\n");
      repo.write("stray.txt", "not the guide");
      await propose(github, repo.dir, tags, remote);
      assert.equal(await pushed(), proposalTitle(tags));
      assert.equal(proposalTitle(tags), "docs(upgrading): name the sections v2.0.0, sdk/go/v0.3.0 shipped");
      assert.equal((await git(["diff-tree", "--no-commit-id", "--name-only", "-r", branch], remote)).trim(), UPGRADING);
      assert.deepEqual(data.created.map((c) => [c.head, c.base]), [[branch, "main"]]);

      // A re-run of the release: the pull request is already open.
      await repo.run("switch", "--quiet", "main");
      await repo.run("branch", "--quiet", "-D", branch);
      repo.write(UPGRADING, "## v2.0.0, sdk/go/v0.3.0 — a change\n");
      await propose(github, repo.dir, tags, remote);
      assert.equal(data.created.length, 1, "an open pull request is not opened again");
    } finally {
      repo.cleanup();
      rmSync(base, { recursive: true, force: true });
    }
  });
});
