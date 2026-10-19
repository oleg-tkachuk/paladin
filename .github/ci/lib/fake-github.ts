// A GitHub that answers from fixed data and records what it was asked, for
// tests.

import type { GitHub, OpenPullRequest } from "./github.ts";

// FakeData is what a fake GitHub answers with, and its log.
export interface FakeData {
  // open are the open pull requests into any base.
  open: OpenPullRequest[];
  // updateFails names the pull requests whose branch GitHub cannot update.
  updateFails: Set<number>;
  dispatched: { eventType: string; payload: Record<string, string> }[];
  autoMerged: number[];
  updated: number[];
}

export function fakeData(): FakeData {
  return { open: [], updateFails: new Set(), dispatched: [], autoMerged: [], updated: [] };
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
  };
}
