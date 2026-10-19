// How the console addresses objects in a batch RPC. Pure, so the grouping and
// the cap are tested once for every bulk action.
import { getOption } from "@bufbuild/protobuf";

import { field as validateField } from "@/gen/buf/validate/validate_pb";
import { ObjectSelectorSchema } from "@/gen/paladin/data/v1/batch_service_pb";
import { celString } from "@/lib/cel";

/**
 * The most names one ObjectSelector takes, read from the contract: past it
 * the request is refused, so a selection that grew past it through "load
 * more" has to go in several.
 */
export const SELECTOR_MAX_NAMES = (() => {
  const r = getOption(ObjectSelectorSchema.field.names, validateField).type;
  if (r.case !== "repeated") {
    throw new Error(
      "ObjectSelector.names has no repeated rules in the contract",
    );
  }
  return Number(r.value.maxItems);
})();

/** An object a bulk action names, and the collection it is in. */
export interface BatchItem {
  name: string;
  collection: string;
}

/** One batch RPC's worth of names, all in one collection. */
export interface SelectorBatch {
  collection: string;
  names: string[];
}

/**
 * The items as batches a selector accepts: one collection each, at most
 * `max` names each, in the order the items came.
 */
export function selectorBatches(
  items: readonly BatchItem[],
  max: number = SELECTOR_MAX_NAMES,
): SelectorBatch[] {
  const byCollection = new Map<string, string[]>();
  for (const it of items) {
    const names = byCollection.get(it.collection) ?? [];
    names.push(it.name);
    byCollection.set(it.collection, names);
  }
  const out: SelectorBatch[] = [];
  for (const [collection, names] of byCollection) {
    for (let i = 0; i < names.length; i += max) {
      out.push({ collection, names: names.slice(i, i + max) });
    }
  }
  return out;
}

/** The CEL variable a copy's key template reads the source key from. */
const SOURCE_KEY = "key";

/**
 * The destination_key_template for a copy: the source key, under a prefix
 * when one is given. The prefix is a string literal, so whatever is typed
 * cannot change the expression around it.
 */
export function copyKeyTemplate(prefix: string): string {
  return prefix ? `${celString(prefix)} + ${SOURCE_KEY}` : SOURCE_KEY;
}

/** Why a bulk copy cannot go where it is aimed, or null when it can. */
export function copyTargetProblem(
  source: string,
  destination: string,
  prefix: string,
): string | null {
  if (!destination) return "Choose the collection to copy into.";
  if (destination === source && !prefix) {
    return "Into the same collection, a prefix is needed: each copy would otherwise be its own source.";
  }
  return null;
}
