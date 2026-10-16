import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

/** What the line says before the client's name. */
export const RELAYED_BY_LABEL = "via";

/**
 * The OAuth client that relayed an audited call on its actor's behalf — the
 * MCP server, for an agent's tool call (ADR-0036). The actor is still the
 * user; this line says the call did not come from them directly. Renders
 * nothing for a direct call.
 */
export function RelayedBy({ client }: { client: string }) {
  if (!client) return null;
  return (
    <div
      className={cn(T.codeSmall, "text-muted-foreground")}
      title={`relayed by the OAuth client ${client}`}
    >
      {RELAYED_BY_LABEL} {client}
    </div>
  );
}
