# .githooks

This directory contains Git hooks for local development.

## pre-push

Runs `make check` (go vet + go test -race + golangci-lint) before every
push. Mirrors CI exactly — if it passes locally, it passes in CI.

One-time setup (run once per checkout):

    git config core.hooksPath .githooks

To skip in an emergency: `git push --no-verify` (not recommended).
