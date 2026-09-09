# Brewfile — the tools this repo's gates shell out to.
#
#     brew bundle
#
# Until now nothing wrote this down: there is no README prerequisites section,
# and each tool announced itself only by failing a precondition with an install
# URL. That is a fine error message and a poor onboarding path.
#
# What is deliberately NOT here matters as much as what is, because a version
# pinned in two places drifts. Each exclusion below names the mechanism that
# owns it instead; scripts/brewfile.test.sh keeps this list and the tools the
# repo actually probes for from parting ways.
#
# not-brew: sqlc        — pinned in backend/go.mod via backend/tools.go (v1.30.0).
#                         The generated code is committed and a pre-commit hook
#                         fails on drift, so a brew-managed sqlc of a different
#                         version would report drift nobody introduced.
# not-brew: mockery     — same, backend/tools.go.
# not-brew: goimports   — go install golang.org/x/tools/cmd/goimports@latest
# not-brew: gosec       — go install github.com/securego/gosec/v2/cmd/gosec@latest
# not-brew: govulncheck — go install golang.org/x/vuln/cmd/govulncheck@latest
# not-brew: node        — nvm locally; the console image pins ARG NODE_VERSION.
# A formula whose binary is named differently says so as `# bin: <name>`;
# every other trailing comment is prose.
#
# not-brew: pnpm        — corepack, from `packageManager` in frontend/package.json.
#                         That version is already pinned twice (package.json and
#                         the image build) with `task deps:check:pnpm` asserting
#                         the pair agrees. A third pin is one more to disagree.

# ─── the commit gate: task verify-all ───────────────────────────────────────
brew "go"
brew "go-task"          # bin: task
brew "golangci-lint"
brew "buf"
brew "helm"
brew "yq"               # chart-netpol.py needs YAML; jq reads only JSON

# The chart checkers are Python, and stay compatible with the python3 macOS
# ships (3.9) so this file does not have to own an interpreter. `from __future__
# import annotations` is what buys that — keep it in new scripts.

# ─── the git hooks: lefthook.yml ────────────────────────────────────────────
brew "lefthook"
brew "gitleaks"

# ─── clusters and releases: verify-deep, deploy, k8s:* ──────────────────────
brew "kubernetes-cli"   # bin: kubectl
brew "kubeconform"      # release:chart:validate
brew "cosign"           # release signing; skip with COSIGN_SIGN=0

# ─── opt-in scans: task sec:* ───────────────────────────────────────────────
# Not wired into any gate on purpose — these are here to be run, not to change
# what an existing gate does (see the `sec:` include in Taskfile.yaml).
brew "trivy"
brew "hadolint"
brew "syft"

# ─── the container runtime ──────────────────────────────────────────────────
# Any Docker-compatible runtime works; this is the one the K8S_CONTEXT default
# (`orbstack`) and the deploy path assume. Remove it if you bring your own.
cask "orbstack"
