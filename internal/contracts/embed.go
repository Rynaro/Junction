// Package contracts embeds the ECL v1.0 directed-edge contract YAML files
// (Eidolon→Eidolon and the six F-HUMAN-EDGE human→Eidolon contracts).
//
// These files are vendored from eidolons-ecl at the version in VERSION, plus
// the six human-to-<eidolon>.yaml files authored as part of Junction F1
// (S19a). They are embedded at build time so the Junction binary is fully
// self-contained and verifiable without network access.
//
// The FS is exposed as Contracts for use by contract.NewRegistryFromFS.
package contracts

import "embed"

// Contracts is the embedded fs.FS containing all *.yaml files in this
// directory. Pass it to contract.NewRegistryFromFS(".", Contracts) to build
// the default registry.
//
//go:embed *.yaml
var Contracts embed.FS
