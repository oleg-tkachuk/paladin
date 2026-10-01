import Link from "next/link";

import { RenamedSlugHint } from "@/components/features/tenants/RenamedSlugHint";

// Rendered when the tenant layout calls notFound() — i.e. the slug (or UUID)
// in the URL matches no visible tenant. Beyond the standard 404 chrome it
// mounts RenamedSlugHint, which offers a redirect when the slug was simply
// renamed (a stale bookmark) rather than genuinely gone.
export default function TenantNotFound() {
  return (
    <div className="flex min-h-[60vh] flex-col items-center justify-center px-6 animate-fade-in">
      <div className="mb-4 select-none text-8xl font-bold tracking-tighter text-white/5">
        404
      </div>
      <h1 className="mb-3 text-2xl font-bold tracking-tight text-white">
        Workspace Not Found
      </h1>
      <p className="mb-8 max-w-md text-center text-sm text-muted-foreground">
        This workspace doesn’t exist, or its address has changed.
      </p>
      <Link
        href="/"
        className="rounded-2xl bg-primary px-6 py-3 text-sm font-bold text-primary-foreground shadow-lg shadow-primary/20 transition-all hover:bg-primary active:scale-95"
      >
        Return to Dashboard
      </Link>
      <RenamedSlugHint />
    </div>
  );
}
