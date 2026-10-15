#!/usr/bin/env bash
# e2e-cluster.test.sh — frontend/scripts/e2e-cluster.sh runs Playwright from the
# console's package wherever it is started, and a spec path still names the
# file the caller meant.
#
# Started from the repository root, it used to run Playwright there: with no
# config in sight Playwright collected the console's vitest files and failed
# with "Vitest cannot be imported in a CommonJS module", which reads like a
# broken suite rather than a wrong directory.
#
# kubectl, curl and npx are stubs, so this needs no cluster and no browser.
# Runs from `task -t Taskfile.dev.yaml verify-all` — seconds.

set -euo pipefail

root=$(git rev-parse --show-toplevel)
script="$root/frontend/scripts/e2e-cluster.sh"
spec=tests/e2e/auth.spec.ts
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

mkdir "$work/bin"
# kubectl answers the deployment check; the password and admin URL come from
# the environment, so nothing else is asked of it.
printf '#!/usr/bin/env bash\nexit 0\n' >"$work/bin/kubectl"
printf '#!/usr/bin/env bash\nexit 0\n' >"$work/bin/curl"
# npx records where it ran and what it was given, one argument per line.
cat >"$work/bin/npx" <<STUB
#!/usr/bin/env bash
pwd >"$work/cwd"
printf '%s\n' "\$@" >"$work/args"
STUB
chmod +x "$work/bin/"*

fail=0
# run <dir> <args...>: the script started from dir, with args.
run() {
    local dir=$1
    shift
    (cd "$dir" && PATH="$work/bin:$PATH" ADMIN_PASSWORD=stub ADMIN_URL=http://stub \
        bash "$script" "$@" >/dev/null)
}
expect() {
    local what=$1 want=$2 got=$3
    if [[ "$got" != "$want" ]]; then
        echo "!!! $what: got '$got', want '$want'"
        fail=1
    fi
}

for start in "$root" "$root/frontend"; do
    rel=${spec}
    [[ "$start" == "$root" ]] && rel=frontend/$spec
    run "$start" "$rel" --headed
    expect "cwd when started from ${start#"$root"}/" "$root/frontend" "$(cat "$work/cwd")"
    expect "spec path from ${start#"$root"}/" "$root/frontend/$spec" "$(sed -n 3p "$work/args")"
    expect "an option passed through" "--headed" "$(sed -n 4p "$work/args")"
done

# A filter that is not a path reaches Playwright as given.
run "$root" shell
expect "a name filter" "shell" "$(sed -n 3p "$work/args")"

if [[ $fail -ne 0 ]]; then
    exit 1
fi
echo "e2e-cluster.sh runs Playwright from frontend/ wherever it is started"
