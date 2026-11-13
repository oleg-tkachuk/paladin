import type { ReactNode } from "react";

import { placeholderParams } from "@/lib/navigation/staticParams";

import CollectionDetailLayout from "./ClientLayout";

// The static export builds this segment once, under the placeholder; the
// layout itself is a client component, which cannot export the params.
export function generateStaticParams() {
  return placeholderParams("name");
}

export default function Layout({ children }: { children: ReactNode }) {
  return <CollectionDetailLayout>{children}</CollectionDetailLayout>;
}
