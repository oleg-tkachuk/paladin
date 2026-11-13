import type { ReactNode } from "react";

import { placeholderParams } from "@/lib/navigation/staticParams";

// The static export builds this segment once, under the placeholder; the
// pages read the real value from the path (lib/navigation/routeParams).
export function generateStaticParams() {
  return placeholderParams("user");
}

export default function Layout({ children }: { children: ReactNode }) {
  return children;
}
