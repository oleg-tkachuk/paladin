// What the commands read from and do to GitHub, behind a narrow interface
// they are tested against, and its implementation over Octokit, GitHub's own
// client, whose retry and throttling plugins answer a transient failure or a
// rate limit.

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
}

// MERGE_METHOD is how a queued pull request merges: a merge commit.
const MERGE_METHOD = "MERGE";
// PAGE_SIZE is the most items the API returns in one page.
export const PAGE_SIZE = 100;

const ENABLE_AUTO_MERGE = `
  mutation($id: ID!, $method: PullRequestMergeMethod!) {
    enablePullRequestAutoMerge(input: { pullRequestId: $id, mergeMethod: $method }) { clientMutationId }
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
      await octokit.graphql(ENABLE_AUTO_MERGE, { id: data.node_id, method: MERGE_METHOD });
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
  };
}

// gitHubFor is the GitHub of the repository fullName, owner/name, acting
// with token.
export function gitHubFor(token: string, fullName: string): GitHub {
  return gitHubOver(new Octokit({ auth: token }), parseRepo(fullName));
}
