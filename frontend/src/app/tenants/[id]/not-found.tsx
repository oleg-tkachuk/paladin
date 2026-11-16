import Link from "next/link";

import { PRESSED } from "@/lib/ui/states";
import { cn } from "@/lib/utils";

// Rendered when the tenant layout calls notFound() — i.e. the slug (or UUID)
// in the URL matches no visible tenant. A slug never changes, so a link that
// worked once and 404s now names a tenant that is gone or out of reach.
export default function TenantNotFound() {
  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center px-6 animate-fade-in">
      <div className="mb-4 select-none text-display font-bold tracking-tighter text-foreground/5">
        404
      </div>
      <h1 className="mb-3 text-2xl font-bold tracking-tight text-foreground">
        Workspace Not Found
      </h1>
      <p className="mb-8 max-w-md text-center text-sm text-muted-foreground">
        This workspace doesn’t exist, or you cannot see it.
      </p>
      <Link
        href="/"
        className={cn(
          "rounded-2xl bg-primary px-6 py-3 text-sm font-bold text-primary-foreground shadow-raised transition-all hover:bg-primary",
          PRESSED,
        )}
      >
        Return to Dashboard
      </Link>
    </div>
  );
}
