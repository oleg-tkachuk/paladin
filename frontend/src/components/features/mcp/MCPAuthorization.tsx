"use client";

import { Badge } from "@/components/ui/badge";
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
  return (
    <div className="space-y-4">
      <Card className="space-y-3 p-4">
        <h3 className={T.cardTitleProse}>How agents authorize</h3>
        <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
          <div>
            <dt className={T.label}>Authorization Server</dt>
            <dd>
              <Badge
                variant={
                  oauth?.authorizationServerEnabled ? "success" : "outline"
                }
              >
                {oauth?.authorizationServerEnabled ? "enabled" : "disabled"}
              </Badge>
            </dd>
          </div>
          <div>
            <dt className={T.label}>OAuth at the MCP server</dt>
            <dd>
              <Badge variant={oauth?.edgeOauthEnabled ? "success" : "outline"}>
                {oauth?.edgeOauthEnabled ? "enabled" : "disabled"}
              </Badge>
            </dd>
          </div>
          <div>
            <dt className={T.label}>Resource URL</dt>
            <dd className={T.code}>{oauth?.resourceUrl || "—"}</dd>
          </div>
          <div>
            <dt className={T.label}>Accepted audience</dt>
            <dd className={T.code}>
              {exchanging ? oauth.edgeAudience : "any plane's token"}
            </dd>
          </div>
          <div>
            <dt className={T.label}>Exchanges tokens as</dt>
            <dd className={T.code}>{oauth?.exchangeClientId || "—"}</dd>
          </div>
        </dl>
      </Card>

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
                <TableRow key={c.clientId}>
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
    </div>
  );
}
