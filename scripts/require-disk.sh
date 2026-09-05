#!/usr/bin/env bash
# Disk preflight for the slow gates, as a command so a Taskfile can run it
# before the first step that consumes anything.
#
# The library it calls lives in scripts/stack-disk.sh, beside the ports one and
# for the same reason. This wrapper exists only because `stack_ensure_disk` was
# first called from inside verify-stack.sh, which runs after the image build
# and after the integration suite — too late to prevent either.
set -euo pipefail
root=$(git rev-parse --show-toplevel)
# shellcheck source=SCRIPTDIR/stack-disk.sh
source "$root/scripts/stack-disk.sh"
# The numbers live here and nowhere else.
#
# kubelet evicts below 10% of the Docker VM disk — 7.3GiB of 72.9GiB on this
# machine — and that is the line that taints the node and leaves every pod in
# every namespace Pending. 10GiB leaves ~2.7GiB of margin for a stack boot;
# the first attempt used 12 and refused a run on a reading that was 14GiB five
# minutes later, because it measured right after the integration suite while
# testcontainers was still cleaning up.
#
# Overridable per-run, for a machine whose disk is a different size.
stack_ensure_disk "${1:-this gate}" "${PALADIN_MIN_FREE_GIB:-10}" "${PALADIN_TARGET_FREE_GIB:-16}"
