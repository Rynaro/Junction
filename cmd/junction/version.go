package main

// Commit is the full git SHA injected at build time via
// -ldflags "-X main.Commit=<sha>".
var Commit string

// Date is the RFC-3339 UTC build timestamp injected at build time via
// -ldflags "-X main.Date=<rfc3339>".
var Date string
