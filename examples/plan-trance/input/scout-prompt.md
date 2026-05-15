# Scout the Junction dispatch path

Produce a seam report covering:

1. The Executor interface contract (`internal/dispatch/dispatch.go`).
2. Concrete executor implementations (Shell, Container, Chain, Fanout).
3. The point in `cmd/junction/main.go` where executor selection happens.
4. Any greenfield package slots in `internal/` relevant to plan-driven dispatch.

This artifact is a starting prompt for the TRANCE chain example
(ATLAS → SPECTRA → APIVR-Δ).
