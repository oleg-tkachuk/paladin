// The texts a policy layer has held, as the console reads them: one paged
// list per layer, under one key, so a save on any page that edits the layer
// reads its history again.
import { infiniteQueryOptions, type QueryClient } from "@tanstack/react-query";
import { Code, ConnectError } from "@connectrpc/connect";

import type { PolicyVersion } from "@/gen/paladin/admin/v1/policy_service_pb";
import { notFoundIsAnswer } from "@/lib/connect/expected";
import { policyClient } from "@/lib/connect/client";

/** Every layer's history, whichever layer. */
export const POLICY_VERSIONS_QUERY_KEY = ["policyVersions"] as const;

/** Versions one page of a layer's history holds. */
export const POLICY_VERSIONS_PAGE_SIZE = 20;

/** One layer's history, by the layer's resource name. */
export const policyVersionsKey = (resourceName: string) =>
  [...POLICY_VERSIONS_QUERY_KEY, resourceName] as const;

/**
 * One page of a layer's history. NotFound — a bucket or collection that does
 * not exist, or that the caller cannot see — is an answer, not a failure: the
 * layer has no history to show.
 */
export interface PolicyVersionsPage {
  versions: PolicyVersion[];
  nextPageToken: string;
  notFound: boolean;
}

async function fetchPolicyVersionsPage(
  resourceName: string,
  pageToken: string,
  signal?: AbortSignal,
): Promise<PolicyVersionsPage> {
  try {
    const res = await policyClient.listPolicyVersions(
      {
        resourceName,
        page: { pageSize: POLICY_VERSIONS_PAGE_SIZE, pageToken },
      },
      notFoundIsAnswer({ signal }),
    );
    return {
      versions: res.versions,
      nextPageToken: res.page?.nextPageToken ?? "",
      notFound: false,
    };
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.NotFound) {
      return { versions: [], nextPageToken: "", notFound: true };
    }
    throw err;
  }
}

/** A layer's history, newest first, a page at a time. */
export function policyVersionsQuery(resourceName: string) {
  return infiniteQueryOptions({
    queryKey: policyVersionsKey(resourceName),
    enabled: resourceName !== "",
    retry: false, // NotFound is answered; a refused read will not change.
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      fetchPolicyVersionsPage(resourceName, pageParam, signal),
    getNextPageParam: (last) => last.nextPageToken || undefined,
  });
}

/** Reads a layer's history again: called by every save of that layer. */
export function invalidatePolicyVersions(
  qc: QueryClient,
  resourceName: string,
): Promise<void> {
  return qc.invalidateQueries({ queryKey: policyVersionsKey(resourceName) });
}
