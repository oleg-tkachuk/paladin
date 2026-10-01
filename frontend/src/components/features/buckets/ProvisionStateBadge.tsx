import { Badge } from "@/components/ui/badge";
import { T } from "@/lib/ui/typography";
import { cn } from "@/lib/utils";

/** A bucket's provisioning state, as the platform and tenant tables show it. */
export function ProvisionStateBadge({ state }: { state: string }) {
  const s = state || "ready";
  switch (s) {
    case "ready":
      return (
        <Badge
          variant="outline"
          className={cn(T.code, "text-muted-foreground")}
        >
          ready
        </Badge>
      );
    case "pending":
      return (
        <Badge variant="info" className={T.code}>
          provisioning…
        </Badge>
      );
    case "deleting":
      return (
        <Badge variant="warning" className={T.code}>
          deleting…
        </Badge>
      );
    case "failed":
      return (
        <Badge variant="destructive" className={T.code}>
          failed
        </Badge>
      );
    case "deletion_failed":
      return (
        <Badge variant="destructive" className={T.code}>
          delete failed
        </Badge>
      );
    default:
      return (
        <Badge variant="outline" className={T.code}>
          {s}
        </Badge>
      );
  }
}
