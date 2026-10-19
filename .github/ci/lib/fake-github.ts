// A GitHub that answers from fixed data and records what it was asked, for
// tests.

import type { GitHub, NewRelease, OpenPullRequest } from "./github.ts";

// FakeData is what a fake GitHub answers with, and its log.
export interface FakeData {
  // open are the open pull requests into any base.
  open: OpenPullRequest[];
  // updateFails names the pull requests whose branch GitHub cannot update.
  updateFails: Set<number>;
  dispatched: { eventType: string; payload: Record<string, string> }[];
  autoMerged: number[];
  updated: number[];
  // conclusions are the push runs' conclusions by sha; queries logs each
  // query as workflow branch sha.
  conclusions: Record<string, string[]>;
  queries: string[];
  // released are the tags with a release; published, the releases asked for.
  released: Set<string>;
  published: NewRelease[];
  // pullsFrom are the open pull requests by head branch; created, the ones
  // asked for.
  pullsFrom: Map<string, number>;
  created: { head: string; base: string; title: string; body: string }[];
}

export function fakeData(): FakeData {
  return {
    open: [],
    updateFails: new Set(),
    dispatched: [],
    autoMerged: [],
    updated: [],
    conclusions: {},
    queries: [],
    released: new Set(),
    published: [],
    pullsFrom: new Map(),
    created: [],
  };
}

export function fakeGitHub(data: FakeData): GitHub {
  return {
    async dispatch(eventType, payload) {
      data.dispatched.push({ eventType, payload });
    },
    async enableAutoMerge(number) {
      data.autoMerged.push(number);
    },
    async openPullRequestsInto() {
      return data.open;
    },
    async updateBranch(number) {
      if (data.updateFails.has(number)) {
        throw new Error(`#${number} cannot be updated`);
      }
      data.updated.push(number);
    },
    async conclusionsOf(workflow, branch, sha) {
      data.queries.push(`${workflow} ${branch} ${sha}`);
      return data.conclusions[sha] ?? [];
    },
    async releaseExists(tag) {
      return data.released.has(tag);
    },
    async publishRelease(release) {
      data.published.push(release);
      data.released.add(release.tag);
    },
    async openPullRequestFrom(head) {
      return data.pullsFrom.get(head);
    },
    async createPullRequest(head, base, title, body) {
      data.created.push({ head, base, title, body });
      const number = data.pullsFrom.size + 1;
      data.pullsFrom.set(head, number);
      return number;
    },
  };
}
