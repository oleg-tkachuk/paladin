import consoleContract from "../../../server/contract/contract.json";

/**
 * The value every dynamic segment is exported under (ADR-0049). The console's
 * server maps any value of the segment to the page built for this one, and the
 * page reads the real value from the path (routeParams.ts).
 */
export const PLACEHOLDER_SEGMENT = consoleContract.placeholderSegment;

/** A segment's generateStaticParams: the one page, under the placeholder. */
export function placeholderParams<K extends string>(
  name: K,
): Array<Record<K, string>> {
  return [{ [name]: PLACEHOLDER_SEGMENT } as Record<K, string>];
}
