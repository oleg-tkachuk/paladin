import { cn } from "@/lib/utils";
import { T } from "@/lib/ui/typography";

import { parseAuditClient } from "./client";

/**
 * The client an audited call came from: the application when it named
 * itself, and the SDK that carried it beneath. The User-Agent as sent is the
 * tooltip.
 */
export function ClientCell({ userAgent }: { userAgent: string }) {
  const client = parseAuditClient(userAgent);
  if (!client) return <span className="text-muted-foreground">—</span>;
  return (
    <div className="min-w-0 max-w-48 space-y-0.5" title={userAgent}>
      <div className={cn(T.body, "truncate")}>{client.name}</div>
      {client.via && <div className={cn(T.hint, "truncate")}>{client.via}</div>}
    </div>
  );
}
