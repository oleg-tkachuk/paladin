// File-type classification + icon picker. Shared by the file list,
// preview pane, and the upload progress UI so MIME-driven affordances
// stay consistent. Centralises both the `kind` enum (image/text/pdf/
// archive/etc.) and the icon mapping — touch one place to add a new
// file class.

import {
  ArchiveBoxIcon,
  CodeBracketIcon,
  DocumentIcon,
  DocumentTextIcon,
  FilmIcon,
  MusicalNoteIcon,
  PhotoIcon,
  TableCellsIcon,
} from "@heroicons/react/24/outline";

export type FileKind =
  | "image"
  | "video"
  | "audio"
  | "text"
  | "json"
  | "pdf"
  | "archive"
  | "spreadsheet"
  | "code"
  | "other";

const EXT_KIND: Record<string, FileKind> = {
  // images
  png: "image",
  jpg: "image",
  jpeg: "image",
  gif: "image",
  webp: "image",
  svg: "image",
  heic: "image",
  // video
  mp4: "video",
  mov: "video",
  webm: "video",
  // audio
  mp3: "audio",
  wav: "audio",
  ogg: "audio",
  flac: "audio",
  // text
  txt: "text",
  md: "text",
  log: "text",
  // structured
  json: "json",
  yaml: "json",
  yml: "json",
  toml: "json",
  // pdf
  pdf: "pdf",
  // archives
  zip: "archive",
  tar: "archive",
  gz: "archive",
  bz2: "archive",
  "7z": "archive",
  // spreadsheets
  csv: "spreadsheet",
  xls: "spreadsheet",
  xlsx: "spreadsheet",
  // code
  go: "code",
  ts: "code",
  tsx: "code",
  js: "code",
  jsx: "code",
  py: "code",
  rs: "code",
  sql: "code",
  sh: "code",
};

// classifyFile picks a FileKind from (contentType, filename). MIME
// wins when present (server-supplied); falls back to the filename
// extension. Returns "other" when nothing matches — caller picks a
// generic icon.
export function classifyFile(
  contentType: string | undefined,
  filename: string | undefined,
): FileKind {
  const ct = (contentType || "").toLowerCase();
  if (ct.startsWith("image/")) return "image";
  if (ct.startsWith("video/")) return "video";
  if (ct.startsWith("audio/")) return "audio";
  if (ct === "application/pdf") return "pdf";
  if (ct === "application/json" || ct.endsWith("+json")) return "json";
  if (ct.startsWith("text/")) return "text";
  if (ct === "application/zip" || ct.includes("compressed")) return "archive";
  if (ct.includes("spreadsheet") || ct.includes("excel")) return "spreadsheet";

  // Filename-extension fallback.
  const ext = (filename || "").split(".").pop()?.toLowerCase();
  if (ext && EXT_KIND[ext]) return EXT_KIND[ext];
  return "other";
}

const KIND_ICON: Record<FileKind, React.ElementType> = {
  image: PhotoIcon,
  video: FilmIcon,
  audio: MusicalNoteIcon,
  text: DocumentTextIcon,
  json: CodeBracketIcon,
  pdf: DocumentIcon,
  archive: ArchiveBoxIcon,
  spreadsheet: TableCellsIcon,
  code: CodeBracketIcon,
  other: DocumentIcon,
};

const KIND_TINT: Record<FileKind, string> = {
  image: "text-chart-2",
  video: "text-chart-4",
  audio: "text-chart-5",
  text: "text-muted-foreground",
  json: "text-chart-3",
  pdf: "text-destructive/80",
  archive: "text-amber-500",
  spreadsheet: "text-chart-1",
  code: "text-chart-3",
  other: "text-muted-foreground",
};

export function iconForKind(kind: FileKind): React.ElementType {
  return KIND_ICON[kind];
}

export function tintForKind(kind: FileKind): string {
  return KIND_TINT[kind];
}

// Human-readable byte formatting. Used in the file list size column
// and in preview tooltips. SI base-10 because storage sizes are
// quoted that way in S3 docs; binary (KiB/MiB) reserved for
// memory contexts.
export function formatBytes(bytes: number | bigint | undefined | null): string {
  if (bytes == null) return "—";
  const n = typeof bytes === "bigint" ? Number(bytes) : bytes;
  if (!isFinite(n) || n < 0) return "—";
  if (n < 1000) return `${n} B`;
  const units = ["kB", "MB", "GB", "TB", "PB"];
  let v = n;
  let i = -1;
  do {
    v /= 1000;
    i++;
  } while (v >= 1000 && i < units.length - 1);
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}
