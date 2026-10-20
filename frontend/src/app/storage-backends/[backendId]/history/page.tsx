"use client";

// A backend's history: the audit entries that name the backend itself —
// registering it, changing it, testing it, its maintenance and credentials.
// Its buckets each have a history of their own.

import { useParams } from "next/navigation";

import { HistoryTimeline } from "@/components/features/history/HistoryTimeline";
import { useBackends } from "@/hooks/useBackends";
import { useResourceHistory } from "@/hooks/useResourceHistory";
import { auditFilterFor } from "@/lib/history";

import { BackendHeader } from "../BackendHeader";

export default function BackendHistoryPage() {
  const params = useParams<{ backendId: string }>();
  const backendId = decodeURIComponent(params.backendId);
  // The header names the backend; until the list loads, or if it fails, it
  // shows the id, which the history needs no more than.
  const { backends } = useBackends();
  const backend = backends.find((b) => b.backendId === backendId);
  const history = useResourceHistory({
    kind: "resource",
    filter: auditFilterFor(`storageBackends/${backendId}`, false),
  });
  return (
    <div className="space-y-6">
      <BackendHeader backendId={backendId} backend={backend} />
      <HistoryTimeline
        history={history}
        what="this backend"
        description="Changes to the backend itself, newest first. Each bucket on it has a history of its own."
      />
    </div>
  );
}
