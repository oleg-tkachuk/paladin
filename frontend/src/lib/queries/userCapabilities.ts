// The capabilities acting on a user's behalf, as
// UserService.ListUserCapabilities pages them, newest first: one key per user,
// grant and set of states.
import { infiniteQueryOptions } from "@tanstack/react-query";

import type { UserCapability_State } from "@/gen/paladin/iam/v1/user_service_pb";
import { userClient } from "@/lib/connect/client";

/** Every user's capabilities, whichever user. */
export const USER_CAPABILITIES_QUERY_KEY = ["userCapabilities"] as const;

/** Capabilities one page shows. */
export const USER_CAPABILITIES_PAGE_SIZE = 50;

/** What a listing asks for. */
export interface UserCapabilitiesFilter {
  /** The user's resource name, tenants/<tenant>/users/<user>. */
  userName: string;
  /** One grant's only; empty for every grant's. */
  grant: string;
  states: readonly UserCapability_State[];
}

export const userCapabilitiesKey = (f: UserCapabilitiesFilter) =>
  [...USER_CAPABILITIES_QUERY_KEY, f.userName, f.grant, [...f.states]] as const;

/** A user's capabilities, a page at a time. */
export function userCapabilitiesQuery(f: UserCapabilitiesFilter) {
  return infiniteQueryOptions({
    queryKey: userCapabilitiesKey(f),
    enabled: f.userName !== "",
    retry: false,
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      userClient.listUserCapabilities(
        {
          name: f.userName,
          grantId: f.grant,
          states: [...f.states],
          pageSize: USER_CAPABILITIES_PAGE_SIZE,
          pageToken: pageParam,
        },
        { signal },
      ),
    getNextPageParam: (last) => last.nextPageToken || undefined,
  });
}
