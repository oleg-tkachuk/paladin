# shellcheck shell=bash
# Disk headroom for the gates that build images and boot stacks.
#
# Sourced by frontend/scripts/verify-e2e.sh and backend/scripts/verify-stack.sh,
# beside stack-ports.sh and for the same reason: a precondition both share, in
# one place, so the two cannot disagree about it.
#
# WHY THIS EXISTS. Three verify-e2e runs in a row, each rebuilding both images,
# grew the buildx cache by ~11GB. Free space on the Docker VM fell to 6.0GiB
# against a 72.9GiB disk — below kubelet's default eviction threshold of 10%
# (7.29GiB) — so the node took a disk-pressure taint, evicted two pods, and left
# every pod in every namespace Pending: argocd, hatchet, zitadel, the lot. The
# gate passed. The cluster did not.
#
# Nothing in the run said so. `docker system df` was already broken by an
# unrelated snapshot corruption, and the only symptom was 48 Pending pods in a
# terminal nobody was watching.
#
# NOT a post-run prune. Pruning after every gate throws away the cache that
# makes the next build fast, to solve a problem that only exists when the disk
# is nearly full. `--min-free-space` reclaims exactly as much as it takes to
# reach the target and no more, so the cache stays warm while there is room.

# Free space, in whole GiB, on the filesystem backing the Docker VM.
#
# `df` on the host rather than kubelet's own numbers: the gates must work with
# no cluster at all, and the two track together anyway — OrbStack's VM disk is
# a sparse file on this volume, and the node reported 11.0Gi available at the
# same moment df did.
stack_free_gib() {
    df -g / 2>/dev/null | awk 'NR==2 {print $4}'
}

# Make room if there is not enough, and say so either way.
#
#   stack_ensure_disk "task -t Taskfile.dev.yaml verify-e2e" \
#       <min_gib> <target_gib>
#
# Reclaims before refusing, because a gate that stops with "free some space"
# when it could have freed the space itself is a gate people learn to work
# around.
stack_ensure_disk() {
    local what="${1:?what is asking}"
    # Both required, no defaults. Defaults here plus explicit values in
    # scripts/require-disk.sh would be two homes for one number, which is the
    # drift this repo keeps paying for — six port numbers in two files, three
    # idempotency prefix lists, a CI timeout the local task did not share.
    local min_gib="${2:?minimum free GiB}"
    local target_gib="${3:?reclaim target GiB}"

    local free
    free=$(stack_free_gib)
    if [[ -z "$free" ]]; then
        echo ">>> [disk] cannot read free space; continuing without the check" >&2
        return 0
    fi
    if ((free >= min_gib)); then
        return 0
    fi

    echo ">>> [disk] ${free}GiB free, want ${min_gib}GiB — reclaiming build cache"
    # Only the build cache. NOT `docker system prune`: on a machine running the
    # OrbStack cluster, most stopped containers are kubelet's own `k8s_*` pod
    # containers, and pruning those deletes records the cluster is tracking.
    docker buildx prune -f --min-free-space "${target_gib}GB" >/dev/null 2>&1 || true

    free=$(stack_free_gib)
    if ((free >= min_gib)); then
        echo ">>> [disk] ${free}GiB free after reclaim"
        return 0
    fi

    {
        echo "!!! ${what} needs about ${min_gib}GiB free and has ${free}GiB."
        echo
        echo "    Reclaiming the build cache was not enough. Running anyway"
        echo "    risks crossing kubelet's eviction threshold (10% of the"
        echo "    Docker VM disk), which taints the node and leaves every pod"
        echo "    in every namespace Pending — including ones this repo does"
        echo "    not own."
        echo
        echo "    What is safe to remove by hand:"
        echo "      docker image prune -a       # images no container uses"
        echo "      docker volume prune         # unreferenced volumes"
        echo
        echo "    What is NOT: docker system prune / container prune. Most"
        echo "    stopped containers here belong to kubelet."
    } >&2
    return 1
}
