"use client";

import Link from "next/link";

import { Badge } from "@/components/ui/badge";
import {
  FeatureSupport,
  type StorageBackend,
} from "@/gen/paladin/admin/v1/types_pb";
import { backendHref } from "@/lib/navigation/hrefs";
import { compatibilityBadge } from "@/lib/storageFeatures";

import { FEATURES_TAB } from "./BackendHeader";

/** How many of the backend's probed features it supports. */
export function supportedCount(backend: StorageBackend): number {
  return backend.features.filter((f) => f.support === FeatureSupport.SUPPORTED)
    .length;
}

/**
 * The backend's S3 feature probe in one line — its compatibility and the
 * count — linking to the tab that lists every feature.
 */
export function FeaturesSummary({ backend }: { backend: StorageBackend }) {
  const compat = compatibilityBadge(backend);
  return (
    <Link
      href={`${backendHref(backend.backendId)}/${FEATURES_TAB}`}
      className="flex items-center gap-2 hover:text-primary"
    >
      <Badge variant={compat.variant}>{compat.label}</Badge>
      <span className="text-sm text-muted-foreground">
        {supportedCount(backend)} of {backend.features.length} supported
      </span>
    </Link>
  );
}
