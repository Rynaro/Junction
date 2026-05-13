// Command junction is the production ECL harness binary.
//
// This is a Phase 0 bootstrap stub. APIVR-Δ replaces this entry point in
// F1 (envelope I/O + sequential dispatch). The shape printed here is
// only enough to verify the build/test toolchain wires up end-to-end.
package main

import (
	"fmt"
	"os"
)

// Version is overridden at build time via -ldflags "-X main.Version=...".
// During Phase 0 it stays "0.0.0-bootstrap" so any accidental release
// build is loud about being pre-F1.
var Version = "0.0.0-bootstrap"

func main() {
	fmt.Fprintf(os.Stdout, "junction %s — Phase 0 bootstrap\n", Version)
	fmt.Fprintln(os.Stdout, "See https://github.com/Rynaro/Junction for status.")
}
