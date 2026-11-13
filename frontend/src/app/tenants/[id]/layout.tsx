import type { ReactNode } from "react";

import { placeholderParams } from "@/lib/navigation/staticParams";

import TenantLayout from "./ClientLayout";

// The static export builds this segment once, under the placeholder; the
// layout itself is a client component, which cannot export the params.
export function generateStaticParams() {
  return placeholderParams("id");
}

export default function Layout({ children }: { children: ReactNode }) {
  return <TenantLayout>{children}</TenantLayout>;
}
