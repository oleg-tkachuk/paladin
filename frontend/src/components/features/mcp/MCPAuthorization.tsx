"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { useNotification } from "@/components/ui/Notification";
import { DetailPanel } from "@/components/ui/DetailPanel";
import {
  RecordField,
  RecordFields,
  openableRow,
} from "@/components/ui/RecordDialog";
import { Timestamp } from "@/components/Timestamp";
import { SortableHead, StaticHead } from "@/components/ui/SortHeader";
import { TableToolbar } from "@/components/ui/TableToolbar";
import { useDetailPanel } from "@/hooks/useDetailPanel";
import { useTableView } from "@/hooks/useTableView";
import { PAGE_TABLE } from "@/lib/table/urlState";
import { useTableFacets } from "@/hooks/useTableFacets";
import { useTablePage } from "@/hooks/useTablePage";
import { TablePagination } from "@/components/ui/TablePagination";
import type { Facet } from "@/lib/table/facets";
import type { SortAccessors } from "@/lib/table/view";
import { cn } from "@/lib/utils";

import { Tile } from "./MCPOverview";
import { MCP_INSPECT_KEY } from "./MCPPage";
import { oauthClientClient } from "@/lib/connect/client";
import { errorMessage } from "@/hooks/errorContract";
import { Card } from "@/components/ui/Card";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type {
  MCPInspectResponse,
  MCPOAuthClient,
} from "@/gen/paladin/admin/v1/mcp_inspect_service_pb";
import { T } from "@/lib/ui/typography";

/**
 * The clients to show: every one the Authorization Server holds when it could
 * list them, else the configuration's seed clients.
 */
export function shownClients(
  oauth: MCPInspectResponse["oauth"],
): MCPOAuthClient[] {
  if (!oauth) return [];
  return oauth.registeredClients.length > 0
    ? oauth.registeredClients
    : oauth.seedClients;
}

/** Where a client came from, in the words an operator needs. */
function clientOrigin(c: MCPOAuthClient): string {
  if (c.dynamic) return "by the client itself";
  if (c.configured) return "by the configuration";
  return "by a configuration that no longer lists it";
}

/** The columns of the clients table. */
const CLIENT_COLUMNS = 5;

/** A client's source, as its badge names it, and the badge's variant. */
const CLIENT_SOURCE = {
  configured: { label: "configured", variant: "outline" },
  registered: { label: "registered itself", variant: "info" },
  dropped: { label: "no longer configured", variant: "warning" },
} as const;

/** The badge of a client that skips the consent screen. */
const SKIPS_CONSENT = "skips consent";

/** Which source a client has: seeded, registered itself, or seeded once. */
function clientSource(c: MCPOAuthClient): keyof typeof CLIENT_SOURCE {
  if (c.dynamic) return "registered";
  return c.configured ? "configured" : "dropped";
}

/**
 * A client's statuses, in a column of their own: badges beside an id the
 * width of a UUID wrapped under it and broke the row. They wrap among
 * themselves rather than past the cell.
 */
function ClientSource({ client }: { client: MCPOAuthClient }) {
  const source = CLIENT_SOURCE[clientSource(client)];
  return (
    <div className="flex flex-wrap gap-1.5">
      <Badge variant={source.variant}>{source.label}</Badge>
      {client.skipConsent ? (
        <Badge variant="outline">{SKIPS_CONSENT}</Badge>
      ) : null}
    </div>
  );
}

// What a client is, in the words an operator needs.
const CLIENT_KIND = {
  server: "MCP server",
  public: "public (PKCE)",
  confidential: "confidential",
} as const;

function clientKindKey(c: MCPOAuthClient): keyof typeof CLIENT_KIND {
  if (c.tokenExchange) return "server";
  return c.public ? "public" : "confidential";
}

/** What a client is, in the words an operator needs. */
function clientKind(c: MCPOAuthClient): string {
  return CLIENT_KIND[clientKindKey(c)];
}

// Which clients show: by where they came from, and by what they are.
const CLIENT_FACETS: readonly Facet<MCPOAuthClient>[] = [
  {
    id: "source",
    label: "Source",
    values: (c) => [clientSource(c)],
    options: (Object.keys(CLIENT_SOURCE) as (keyof typeof CLIENT_SOURCE)[]).map(
      (k) => ({ value: k, label: CLIENT_SOURCE[k].label }),
    ),
  },
  {
    id: "kind",
    label: "Kind",
    values: (c) => [clientKindKey(c)],
    options: (Object.keys(CLIENT_KIND) as (keyof typeof CLIENT_KIND)[]).map(
      (k) => ({ value: k, label: CLIENT_KIND[k] }),
    ),
  },
];

type ClientColumn = "client" | "source" | "kind";
const CLIENT_SORT: SortAccessors<MCPOAuthClient, ClientColumn> = {
  client: (c) => c.clientId,
  source: (c) => CLIENT_SOURCE[clientSource(c)].label,
  kind: clientKind,
};
const CLIENT_SEARCH = (c: MCPOAuthClient) => [
  c.clientId,
  clientKind(c),
  c.allowedScopes.join(" "),
  c.allowedAudiences.join(" "),
];

/**
 * How agents authorize (ADR-0036): whether IAM is the OAuth Authorization
 * Server, the audience the MCP server accepts, and the clients registered at
 * boot. Secrets never reach this page — the server does not send them.
 */
export function MCPAuthorization({ inspect }: { inspect: MCPInspectResponse }) {
  const oauth = inspect.oauth;
  const exchanging = !!oauth?.edgeAudience;
  const all = shownClients(oauth);
  const facets = useTableFacets(all, CLIENT_FACETS, PAGE_TABLE);
  const view = useTableView(facets.rows, {
    text: CLIENT_SEARCH,
    accessors: CLIENT_SORT,
    url: PAGE_TABLE,
  });
  const paged = useTablePage(view.rows, {
    url: PAGE_TABLE,
    resetOn: [view.query, facets.selection],
  });
  const clients = paged.rows;
  const sortProps = { current: view.sort, onSort: view.toggleSort };
  // The open client is in the URL, looked up among every client shown, so a
  // link opens one the search or a facet hides. The page's only list, so it
  // takes the bare names like the rest of its view.
  const detail = useDetailPanel(undefined, PAGE_TABLE);
  const opened = all.find((c) => c.clientId === detail.openId) ?? null;
  const [deleting, setDeleting] = useState<MCPOAuthClient | null>(null);
  const queryClient = useQueryClient();
  const { showNotification } = useNotification();
  // Only a client the store holds can be deleted, and not one the
  // configuration seeds: the next boot would register it again.
  const fromStore = (oauth?.registeredClients.length ?? 0) > 0;
  const deletable = (c: MCPOAuthClient) => fromStore && !c.configured;

  const remove = async () => {
    if (!deleting) return;
    try {
      await oauthClientClient.deleteOAuthClient({
        clientId: deleting.clientId,
      });
      showNotification({
        type: "success",
        title: "Client deleted",
        message: `${deleting.clientId} and its grants are gone.`,
      });
    } catch (err) {
      showNotification({
        type: "error",
        title: "Delete failed",
        message: errorMessage(err, "The client was not deleted"),
      });
      throw err;
    } finally {
      void queryClient.invalidateQueries({ queryKey: MCP_INSPECT_KEY });
    }
  };
  return (
    <div className="space-y-4">
      <section className="space-y-3">
        <h2 className={T.cardTitleProse}>How agents authorize</h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <Tile
            label="Authorization Server"
            hint="IAM serves /oauth/authorize and /oauth/token."
          >
            <Badge
              variant={
                oauth?.authorizationServerEnabled ? "success" : "outline"
              }
            >
              {oauth?.authorizationServerEnabled ? "enabled" : "disabled"}
            </Badge>
          </Tile>
          <Tile
            label="OAuth at the MCP server"
            hint="It challenges with 401 and points agents at IAM."
          >
            <Badge variant={oauth?.edgeOauthEnabled ? "success" : "outline"}>
              {oauth?.edgeOauthEnabled ? "enabled" : "disabled"}
            </Badge>
          </Tile>
          <Tile
            label="Accepted audience"
            hint={
              exchanging
                ? "No plane accepts it; the server exchanges it per plane."
                : "The server forwards the token it is given."
            }
          >
            <span className={T.code}>
              {exchanging ? oauth.edgeAudience : "any plane's"}
            </span>
          </Tile>
          <Tile label="Exchanges tokens as" hint="The MCP server's own client.">
            <span className={cn(T.code, "break-all")}>
              {oauth?.exchangeClientId || "—"}
            </span>
          </Tile>
        </div>
        <Card className="space-y-1 p-4">
          <div className={T.label}>Resource URL</div>
          <div className={cn(T.code, "break-all")}>
            {oauth?.resourceUrl || "—"}
          </div>
          <p className={T.hint}>What an agent authorizes for.</p>
        </Card>
      </section>

      {oauth?.registeredClientsError ? (
        <p className={T.hint}>
          Showing the configured clients only: the registered ones could not be
          read ({oauth.registeredClientsError}).
        </p>
      ) : null}
      <TableToolbar
        query={view.query}
        onQueryChange={view.setQuery}
        searchLabel="Search OAuth clients"
        placeholder="Search by client, kind, scope or audience…"
        facets={facets.controls}
        count={{ shown: view.rows.length, total: all.length }}
      />
      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <SortableHead label="Client" column="client" {...sortProps} />
              <SortableHead label="Source" column="source" {...sortProps} />
              <SortableHead
                label="Kind"
                column="kind"
                className="hidden @sm:table-cell"
                {...sortProps}
              />
              <StaticHead className="hidden @md:table-cell">Scopes</StaticHead>
              <StaticHead className="hidden @md:table-cell">
                Audiences
              </StaticHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {clients.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={CLIENT_COLUMNS}
                  className="py-8 text-center text-xs text-muted-foreground"
                >
                  {all.length === 0
                    ? "No client is registered."
                    : "No client matches."}
                </TableCell>
              </TableRow>
            ) : (
              clients.map((c) => (
                <TableRow
                  key={c.clientId}
                  {...openableRow(() => detail.open(c.clientId))}
                  {...detail.rowProps(c.clientId)}
                >
                  <TableCell
                    className={cn(T.code, "whitespace-normal break-all")}
                  >
                    {c.clientId}
                  </TableCell>
                  {/* On a narrow card the source outranks the kind, which the
                      client's record still shows. */}
                  <TableCell>
                    <ClientSource client={c} />
                  </TableCell>
                  <TableCell className="hidden @sm:table-cell">
                    {clientKind(c)}
                  </TableCell>
                  <TableCell
                    className={`hidden @md:table-cell ${T.code} whitespace-normal break-words text-muted-foreground`}
                  >
                    {c.allowedScopes.join(" ") || "—"}
                  </TableCell>
                  <TableCell
                    className={`hidden @md:table-cell ${T.code} whitespace-normal break-words text-muted-foreground`}
                  >
                    {c.allowedAudiences.join(" ") || "—"}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>
      <TablePagination {...paged.pagination} />
      <DetailPanel
        open={opened !== null}
        onClose={detail.close}
        rowId={opened?.clientId}
        title="OAuth client"
        identifier={opened?.clientId}
        footer={
          opened && deletable(opened) ? (
            <Button
              variant="destructive"
              size="sm"
              onClick={() => {
                setDeleting(opened);
                detail.close();
              }}
            >
              Delete client
            </Button>
          ) : null
        }
      >
        {opened ? (
          <RecordFields>
            <RecordField label="Kind">{clientKind(opened)}</RecordField>
            <RecordField label="Registered">
              {clientOrigin(opened)}
              {opened.createdAt ? (
                <>
                  {" · "}
                  <Timestamp ts={opened.createdAt} />
                </>
              ) : null}
            </RecordField>
            <RecordField label="Scopes" mono>
              {opened.allowedScopes.join(" ") || "—"}
            </RecordField>
            <RecordField label="Audiences" mono>
              {opened.allowedAudiences.join(" ") || "—"}
            </RecordField>
            <RecordField label="Consent">
              {opened.skipConsent ? "skipped (trusted client)" : "asked"}
            </RecordField>
            <RecordField label="Token exchange">
              {opened.tokenExchange ? "may exchange tokens" : "—"}
            </RecordField>
            {!deletable(opened) && opened.configured ? (
              <p className={T.hint}>
                Seeded by auth.oauth.seed_clients: remove it there to delete it.
              </p>
            ) : null}
          </RecordFields>
        ) : null}
      </DetailPanel>
      <ConfirmModal
        isOpen={deleting !== null}
        onClose={() => setDeleting(null)}
        onConfirm={remove}
        type="danger"
        title="Delete this OAuth client?"
        message={`${deleting?.clientId ?? "The client"} is removed with every grant issued to it, in every tenant.`}
        warning="Agents using it must register and authorize again. An access token one already holds keeps working for a few minutes."
        confirmText="Delete client"
        // Every grant in every tenant goes with it, so it is typed out; as
        // the verb, since a registered client's id is a generated string.
        typeToConfirm="delete"
      />
    </div>
  );
}
