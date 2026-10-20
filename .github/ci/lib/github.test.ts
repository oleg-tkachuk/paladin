import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { Octokit } from "octokit";

import { gitHubOver, parseRepo } from "./github.ts";

// Request is one call the client made: its method, path and JSON body.
interface Request {
  method: string;
  path: string;
  body: Record<string, unknown> | undefined;
}

// recording is a GitHub of o/r over an Octokit whose every request is logged
// and answered by answer, so a test reads what reached the wire.
function recording(answer: (r: Request) => unknown) {
  const requests: Request[] = [];
  const fetch = async (url: string | URL, init?: RequestInit): Promise<Response> => {
    const request: Request = {
      method: init?.method ?? "GET",
      path: new URL(url).pathname,
      body: typeof init?.body === "string" && init.body !== "" ? (JSON.parse(init.body) as Record<string, unknown>) : undefined,
    };
    requests.push(request);
    const data = answer(request);
    return data === undefined ? new Response(null, { status: 204 }) : new Response(JSON.stringify(data), { headers: { "content-type": "application/json" } });
  };
  const octokit = new Octokit({ request: { fetch }, retry: { enabled: false }, throttle: { enabled: false } });
  return { github: gitHubOver(octokit, parseRepo("o/r")), requests };
}

const GRAPHQL = "/graphql";

describe("parseRepo", () => {
  test("owner/name", () => assert.deepEqual(parseRepo("oleg-tkachuk/paladin"), { owner: "oleg-tkachuk", repo: "paladin" }));
  test("anything else", () => {
    for (const bad of ["paladin", "a/b/c", "", "/x"]) assert.throws(() => parseRepo(bad), /is not owner\/name/, bad);
  });
});

test("enableAutoMerge queues the pull request's node with a merge commit", async () => {
  const { github, requests } = recording((r) => (r.path === GRAPHQL ? { data: { enablePullRequestAutoMerge: { clientMutationId: null } } } : { node_id: "PR_node" }));
  await github.enableAutoMerge(7);
  assert.equal(requests[0]?.path, "/repos/o/r/pulls/7");
  assert.equal(requests[1]?.path, GRAPHQL);
  assert.match(String(requests[1]?.body?.query), /enablePullRequestAutoMerge/);
  assert.deepEqual(requests[1]?.body?.variables, { id: "PR_node", mergeMethod: "MERGE" });
});

test("openPullRequestsInto reads every page", async () => {
  const pages = [
    { nodes: [{ number: 1, isDraft: false, isCrossRepository: false, mergeStateStatus: "BEHIND" }], pageInfo: { hasNextPage: true, endCursor: "c1" } },
    { nodes: [{ number: 2, isDraft: true, isCrossRepository: true, mergeStateStatus: "DIRTY" }], pageInfo: { hasNextPage: false, endCursor: "c2" } },
  ];
  const { github, requests } = recording((r) => {
    const cursor = (r.body?.variables as { cursor?: string } | undefined)?.cursor;
    return { data: { repository: { pullRequests: cursor === undefined ? pages[0] : pages[1] } } };
  });
  assert.deepEqual(await github.openPullRequestsInto("main"), [
    { number: 1, draft: false, fromFork: false, mergeState: "BEHIND" },
    { number: 2, draft: true, fromFork: true, mergeState: "DIRTY" },
  ]);
  assert.equal(requests.length, 2);
  assert.deepEqual(requests[0]?.body?.variables, { owner: "o", repo: "r", base: "main" });
});

test("dispatch and updateBranch reach their endpoints", async () => {
  const { github, requests } = recording(() => undefined);
  await github.dispatch("release", { sha: "abc" });
  await github.updateBranch(9);
  assert.deepEqual(
    requests.map((r) => [r.method, r.path, r.body]),
    [
      ["POST", "/repos/o/r/dispatches", { event_type: "release", client_payload: { sha: "abc" } }],
      ["PUT", "/repos/o/r/pulls/9/update-branch", undefined],
    ],
  );
});

test("a prerelease is published as one, and never as the Latest", async () => {
  const { github, requests } = recording((r) => (r.path.includes("/git/ref/") ? { ref: "x" } : { id: 1 }));
  await github.publishRelease({ tag: "v1.1.0-rc.1", title: "v1.1.0-rc.1", notes: { generated: true }, latest: true, prerelease: true, assets: [] });
  const create = requests.find((r) => r.path === "/repos/o/r/releases");
  assert.equal(create?.body?.prerelease, true);
  assert.equal(create?.body?.make_latest, "false");
});
