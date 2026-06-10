// Package contracts embeds the ECL v2.1.0 directed-edge contract YAML files
// (Eidolon→Eidolon, human→Eidolon, kupo executor, and vivi succession edges).
//
// These files are vendored from eidolons-ecl at the version in VERSION.
// 45 YAML files total: 24 original edges (6 human + 18 Eidolon-to-Eidolon),
// 11 Kupo executor edges (ECL #9), and 10 Vivi succession edges (ECL #10).
// They are embedded at build time so the Junction binary is fully
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
