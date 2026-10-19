// What the commands read from and do to GitHub, behind a narrow interface
// they are tested against, and its implementation over Octokit, GitHub's own
// client, whose retry and throttling plugins answer a transient failure or a
// rate limit.

import { readFileSync } from "node:fs";
import { basename } from "node:path";

import { Octokit } from "octokit";

// Repo is a repository as owner and name.
export interface Repo {
  owner: string;
  repo: string;
}

// parseRepo reads owner/name, as GITHUB_REPOSITORY spells it.
export function parseRepo(fullName: string): Repo {
  const match = /^([\w.-]+)\/([\w.-]+)$/.exec(fullName);
  if (match === null || match[1] === undefined || match[2] === undefined) {
    throw new Error(`${fullName} is not owner/name`);
  }
  return { owner: match[1], repo: match[2] };
}

// MergeState is how far GitHub says a pull request is from mergeable: BEHIND
// is the one state updating its branch fixes; a conflict, DIRTY, needs a
// person.
export const MergeState = { Behind: "BEHIND", Dirty: "DIRTY", Clean: "CLEAN" } as const;

// OpenPullRequest is what the merge automation reads of an open pull request.
export interface OpenPullRequest {
  number: number;
  draft: boolean;
  // fromFork: the head is in another repository than the base.
  fromFork: boolean;
  mergeState: string;
}

// NewRelease is a GitHub release to publish for a tag already pushed.
export interface NewRelease {
  tag: string;
  title: string;
  // notes is the body; generated asks GitHub to write it from the pull
  // requests merged since the previous release, shaped by .github/release.yml.
  notes: { text: string } | { generated: true };
  // latest marks it Latest; otherwise GitHub is told it is not.
  latest: boolean;
  // assets are files attached to it, by path.
  assets: readonly string[];
}

// GitHub is everything the commands ask of the API.
export interface GitHub {
  // dispatch sends a repository_dispatch event of eventType with payload.
  dispatch(eventType: string, payload: Record<string, string>): Promise<void>;
  // enableAutoMerge queues a pull request to merge, with a merge commit, once
  // its required checks pass.
  enableAutoMerge(number: number): Promise<void>;
  // openPullRequestsInto is every open pull request into base.
  openPullRequestsInto(base: string): Promise<OpenPullRequest[]>;
  // updateBranch brings a pull request's branch up to date with its base.
  updateBranch(number: number): Promise<void>;
  // conclusionsOf is how each push run of the workflow file on branch that
  // tested sha ended; an empty string for one still running.
  conclusionsOf(workflow: string, branch: string, sha: string): Promise<string[]>;
  releaseExists(tag: string): Promise<boolean>;
  // publishRelease publishes a release of a tag the repository holds.
  publishRelease(release: NewRelease): Promise<void>;
  // openPullRequestFrom is the open pull request from this repository's
  // branch head, if any: a fork's of the same branch name is not.
  openPullRequestFrom(head: string): Promise<number | undefined>;
  createPullRequest(head: string, base: string, title: string, body: string): Promise<number>;
}

// NOT_FOUND is the status of what the API does not have.
const NOT_FOUND = 404;
// PUSH_EVENT names the runs that tested a commit on its branch.
const PUSH_EVENT = "push";
// MAKE_LATEST_FALSE tells GitHub a release is not the Latest.
const MAKE_LATEST_FALSE = "false";
// ASSET_TYPE is how an attached file is served: the SBOMs are JSON.
const ASSET_TYPE = "application/json";

// statusOf is an Octokit error's HTTP status, if it has one.
function statusOf(err: unknown): number | undefined {
  return typeof err === "object" && err !== null && "status" in err && typeof err.status === "number" ? err.status : undefined;
}

// existsBy reports whether lookup finds what it looks for: a 404 is no, any
// other failure is thrown.
async function existsBy(lookup: () => Promise<unknown>): Promise<boolean> {
  try {
    await lookup();
    return true;
  } catch (err) {
    if (statusOf(err) === NOT_FOUND) {
      return false;
    }
    throw err;
  }
}

// MERGE_METHOD is how a queued pull request merges: a merge commit.
const MERGE_METHOD = "MERGE";
// PAGE_SIZE is the most items the API returns in one page.
export const PAGE_SIZE = 100;

const ENABLE_AUTO_MERGE = `
  mutation($id: ID!, $mergeMethod: PullRequestMergeMethod!) {
    enablePullRequestAutoMerge(input: { pullRequestId: $id, mergeMethod: $mergeMethod }) { clientMutationId }
  }`;

// mergeStateStatus is in GraphQL alone; the REST list leaves it out.
const OPEN_PULL_REQUESTS = `
  query($owner: String!, $repo: String!, $base: String!, $cursor: String) {
    repository(owner: $owner, name: $repo) {
      pullRequests(states: OPEN, baseRefName: $base, first: ${PAGE_SIZE}, after: $cursor) {
        nodes { number isDraft isCrossRepository mergeStateStatus }
        pageInfo { hasNextPage endCursor }
      }
    }
  }`;

interface OpenPullRequestsPage {
  repository: {
    pullRequests: { nodes: { number: number; isDraft: boolean; isCrossRepository: boolean; mergeStateStatus: string }[] };
  };
}

// gitHubOver is the GitHub of repo through octokit.
export function gitHubOver(octokit: Octokit, repo: Repo): GitHub {
  return {
    async dispatch(eventType, payload) {
      await octokit.rest.repos.createDispatchEvent({ ...repo, event_type: eventType, client_payload: payload });
    },
    async enableAutoMerge(number) {
      const { data } = await octokit.rest.pulls.get({ ...repo, pull_number: number });
      await octokit.graphql(ENABLE_AUTO_MERGE, { id: data.node_id, mergeMethod: MERGE_METHOD });
    },
    async openPullRequestsInto(base) {
      const page = await octokit.graphql.paginate<OpenPullRequestsPage>(OPEN_PULL_REQUESTS, { ...repo, base });
      return page.repository.pullRequests.nodes.map((p) => ({
        number: p.number,
        draft: p.isDraft,
        fromFork: p.isCrossRepository,
        mergeState: p.mergeStateStatus,
      }));
    },
    // The REST endpoint takes no update method: it merges the base into the
    // branch, which keeps a branch someone is still pushing to fast-forward.
    async updateBranch(number) {
      await octokit.rest.pulls.updateBranch({ ...repo, pull_number: number });
    },
    async conclusionsOf(workflow, branch, sha) {
      const runs = await octokit.paginate(octokit.rest.actions.listWorkflowRuns, {
        ...repo,
        workflow_id: workflow,
        branch,
        event: PUSH_EVENT,
        head_sha: sha,
        per_page: PAGE_SIZE,
      });
      return runs.map((r) => r.conclusion ?? "");
    },
    async releaseExists(tag) {
      return existsBy(() => octokit.rest.repos.getReleaseByTag({ ...repo, tag }));
    },
    async publishRelease(release) {
      // The tag must be pushed already: a release of a missing tag would
      // create it at the default branch's head.
      if (!(await existsBy(() => octokit.rest.git.getRef({ ...repo, ref: `tags/${release.tag}` })))) {
        throw new Error(`tag ${release.tag} is not in the repository`);
      }
      const { data } = await octokit.rest.repos.createRelease({
        ...repo,
        tag_name: release.tag,
        name: release.title,
        ...("text" in release.notes ? { body: release.notes.text } : { generate_release_notes: true }),
        ...(release.latest ? {} : { make_latest: MAKE_LATEST_FALSE }),
      });
      for (const path of release.assets) {
        await octokit.rest.repos.uploadReleaseAsset({
          ...repo,
          release_id: data.id,
          name: basename(path),
          // The client sends the bytes as given; its type says a string.
          data: readFileSync(path) as unknown as string,
          headers: { "content-type": ASSET_TYPE },
        });
      }
    },
    async openPullRequestFrom(head) {
      const { data } = await octokit.rest.pulls.list({ ...repo, head: `${repo.owner}:${head}`, state: "open" });
      return data.find((p) => p.head.repo?.full_name === `${repo.owner}/${repo.repo}`)?.number;
    },
    async createPullRequest(head, base, title, body) {
      const { data } = await octokit.rest.pulls.create({ ...repo, head, base, title, body });
      return data.number;
    },
  };
}

// gitHubFor is the GitHub of the repository fullName, owner/name, acting
// with token.
export function gitHubFor(token: string, fullName: string): GitHub {
  return gitHubOver(new Octokit({ auth: token }), parseRepo(fullName));
}
