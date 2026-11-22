"use client";

/**
 * The object resources a capability is confined to, picked rather than typed:
 * the tenant is the capability's own, the collection is one of the tenant's,
 * and the key prefix is held to the key rule as it is typed. Each row shows
 * the resource it grants, built by objectResource exactly as the server
 * checks it. No rows is no restriction.
 */
import { useQuery } from "@tanstack/react-query";
import { PlusIcon, XMarkIcon } from "@heroicons/react/24/outline";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/Select";
import { FormField } from "@/components/ui/form-dialog";
import { ListLoadError } from "@/components/ui/ListLoadError";
import { errorMessage } from "@/hooks/errorContract";
import {
  isCollectionName,
  objectGrantProblems,
  objectResource,
  type ObjectGrantDraft,
} from "@/lib/capability/objectResource";
import { collectionsQuery } from "@/lib/queries/collections";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

/** What a row says before it has a collection to build a resource from. */
const NO_RESOURCE_YET = "—";

export const EMPTY_GRANT: ObjectGrantDraft = { collection: "", key: "" };

export const OBJECT_GRANTS_UNRESTRICTED =
  "None: the capability reaches every object of the tenant.";

export const KEY_PREFIX_HINT =
  'Empty grants the whole collection. "contracts/2026/" covers the keys under it; "contracts/2026" also covers that key itself. Listing a collection needs the whole collection.';

export function ObjectResourceGrants({
  tenantId,
  grants,
  onChange,
  enabled = true,
}: {
  /** The capability's tenant, by id: every resource is in it. */
  tenantId: string;
  grants: ObjectGrantDraft[];
  onChange: (next: ObjectGrantDraft[]) => void;
  /** False holds the collections read, as a closed dialog does. */
  enabled?: boolean;
}) {
  const collectionsRead = useQuery({
    ...collectionsQuery(`tenants/${tenantId}`),
    enabled: enabled && !!tenantId,
  });
  const collections = collectionsRead.data ?? [];
  const options = collections.map((c) => ({
    value: c.collection,
    label: c.displayName ? `${c.displayName} (${c.collection})` : c.collection,
  }));
  const problems = objectGrantProblems(tenantId, grants);

  const update = (i: number, patch: Partial<ObjectGrantDraft>) =>
    onChange(grants.map((g, j) => (j === i ? { ...g, ...patch } : g)));
  const remove = (i: number) => onChange(grants.filter((_, j) => j !== i));

  return (
    <div className="space-y-2">
      {collectionsRead.error ? (
        <ListLoadError
          variant="inline"
          what="Collections"
          reason={errorMessage(
            collectionsRead.error,
            "Failed to fetch collections",
          )}
          onRetry={() => void collectionsRead.refetch()}
        />
      ) : collectionsRead.isSuccess && collections.length === 0 ? (
        <p className={T.hint}>
          This tenant has no collections to grant on yet.
        </p>
      ) : null}
      {grants.length === 0 ? (
        <p className={T.hint}>{OBJECT_GRANTS_UNRESTRICTED}</p>
      ) : (
        <ol className="space-y-3">
          {grants.map((grant, i) => {
            const n = i + 1;
            const problem = problems[i];
            const collectionProblem =
              problem && !isCollectionName(grant.collection) ? problem : null;
            const keyProblem = problem && !collectionProblem ? problem : null;
            const resource = isCollectionName(grant.collection)
              ? objectResource(tenantId, grant.collection, grant.key)
              : NO_RESOURCE_YET;
            return (
              <li
                // Rows have no identity beyond their place; every control is
                // controlled, so removing one re-renders the rest from state.
                key={i}
                aria-label={`Resource ${n}`}
                className="space-y-2 rounded-md border p-3"
              >
                <div className="grid gap-3 sm:grid-cols-2">
                  <FormField label="Collection" error={collectionProblem}>
                    {(control) => (
                      <Select
                        {...control}
                        options={options}
                        value={grant.collection}
                        placeholder="Choose a collection"
                        onChange={(collection) => update(i, { collection })}
                      />
                    )}
                  </FormField>
                  <FormField label="Key prefix" error={keyProblem}>
                    {(control) => (
                      <Input
                        {...control}
                        className="font-mono text-xs"
                        placeholder="e.g. contracts/2026/"
                        value={grant.key}
                        onChange={(e) => update(i, { key: e.target.value })}
                      />
                    )}
                  </FormField>
                </div>
                <div className="flex items-center justify-between gap-2">
                  <output
                    aria-label={`Resource ${n} grants`}
                    className={cn(T.code, "break-all text-muted-foreground")}
                  >
                    {resource}
                  </output>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    aria-label={`Remove resource ${n}`}
                    onClick={() => remove(i)}
                  >
                    <XMarkIcon className="size-4" />
                  </Button>
                </div>
              </li>
            );
          })}
        </ol>
      )}
      <div className="flex items-center justify-between gap-2">
        <p className={T.hint}>{KEY_PREFIX_HINT}</p>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onChange([...grants, EMPTY_GRANT])}
        >
          <PlusIcon className="size-4" />
          Add resource
        </Button>
      </div>
    </div>
  );
}
