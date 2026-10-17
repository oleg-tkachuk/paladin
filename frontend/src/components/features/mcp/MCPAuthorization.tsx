"use client";

import { useState } from "react";

import { Badge } from "@/components/ui/badge";
import {
  RecordDialog,
  RecordField,
  openableRow,
} from "@/components/ui/RecordDialog";
import { Timestamp } from "@/components/Timestamp";
import { cn } from "@/lib/utils";

import { Tile } from "./MCPOverview";
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
                <TableRow
                  key={c.clientId}
                  {...openableRow(
                    () => setOpened(c),
                    `Open client ${c.clientId}`,
                  )}
                >
                  <TableCell className={T.code}>
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
                  <TableCell className="text-xs">{clientKind(c)}</TableCell>
                  <TableCell
                    className={`hidden @md:table-cell ${T.code} text-muted-foreground`}
                  >
                    {c.allowedScopes.join(" ") || "—"}
                  </TableCell>
                  <TableCell
                    className={`hidden @md:table-cell ${T.code} text-muted-foreground`}
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
              {opened.dynamic ? "by the client itself" : "by the configuration"}
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
          </>
        ) : null}
      </RecordDialog>
    </div>
  );
}
