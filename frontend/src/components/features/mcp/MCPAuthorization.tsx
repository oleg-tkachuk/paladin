"use client";

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmModal } from "@/components/ui/ConfirmModal";
import { useNotification } from "@/components/ui/Notification";
import {
  RecordDialog,
  RecordField,
  openableRow,
} from "@/components/ui/RecordDialog";
import { Timestamp } from "@/components/Timestamp";
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
  TableHead,
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

/** What a client is, in the words an operator needs. */
function clientKind(c: MCPOAuthClient): string {
  if (c.tokenExchange) return "MCP server";
  return c.public ? "public (PKCE)" : "confidential";
}

/**
 * How agents authorize (ADR-0036): whether IAM is the OAuth Authorization
 * Server, the audience the MCP server accepts, and the clients registered at
 * boot. Secrets never reach this page — the server does not send them.
 */
export function MCPAuthorization({ inspect }: { inspect: MCPInspectResponse }) {
  const oauth = inspect.oauth;
  const exchanging = !!oauth?.edgeAudience;
  const clients = shownClients(oauth);
  const [opened, setOpened] = useState<MCPOAuthClient | null>(null);
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
      <Card className="p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Client</TableHead>
              <TableHead>Kind</TableHead>
              <TableHead className="hidden @md:table-cell">Scopes</TableHead>
              <TableHead className="hidden @md:table-cell">Audiences</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {clients.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={4}
                  className="py-8 text-center text-xs text-muted-foreground"
                >
                  No client is registered.
                </TableCell>
              </TableRow>
            ) : (
              clients.map((c) => (
                <TableRow key={c.clientId} {...openableRow(() => setOpened(c))}>
                  <TableCell
                    className={cn(T.code, "whitespace-normal break-all")}
                  >
                    {c.clientId}
                    {c.dynamic ? (
                      <Badge variant="info" className="ml-2">
                        registered itself
                      </Badge>
                    ) : null}
                    {c.skipConsent ? (
                      <Badge variant="outline" className="ml-2">
                        skips consent
                      </Badge>
                    ) : null}
                  </TableCell>
                  <TableCell>{clientKind(c)}</TableCell>
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
      <RecordDialog
        open={opened !== null}
        onOpenChange={(o) => !o && setOpened(null)}
        title="OAuth client"
        subtitle={opened?.clientId}
      >
        {opened ? (
          <>
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
            {deletable(opened) ? (
              <div className="flex justify-end pt-2">
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => {
                    setDeleting(opened);
                    setOpened(null);
                  }}
                >
                  Delete client
                </Button>
              </div>
            ) : opened.configured ? (
              <p className={T.hint}>
                Seeded by auth.oauth.seed_clients: remove it there to delete it.
              </p>
            ) : null}
          </>
        ) : null}
      </RecordDialog>
      <ConfirmModal
        isOpen={deleting !== null}
        onClose={() => setDeleting(null)}
        onConfirm={remove}
        type="danger"
        title="Delete this OAuth client?"
        message={`${deleting?.clientId ?? "The client"} is removed with every grant issued to it, in every tenant.`}
        warning="Agents using it must register and authorize again. An access token one already holds keeps working for a few minutes."
        confirmText="Delete client"
      />
    </div>
  );
}
