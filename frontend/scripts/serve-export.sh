#!/usr/bin/env bash
# Serve the production build the way the image does: the static export,
# embedded in the console's Go server (ADR-0049). Run `pnpm build` first.
#
#   bash scripts/serve-export.sh        # http://localhost:3000
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export_dir="$here/out"
embed_dir="$here/server/dist/out"
keep=".gitkeep"

if [[ ! -f "$export_dir/index.html" ]]; then
  echo "serve-export: no export at $export_dir; run pnpm build first" >&2
  exit 1
fi

# The embedded tree is replaced whole, so no file of an older build lingers.
find "$embed_dir" -mindepth 1 ! -name "$keep" -exec rm -rf {} +
cp -R "$export_dir/." "$embed_dir/"
exec go -C "$here/server" run ./cmd/paladin-console
