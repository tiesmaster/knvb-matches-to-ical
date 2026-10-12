// Package fixtures embeds real and synthetic Sportlink API responses, used by
// the dummy source and by tests.
package fixtures

import _ "embed"

// PoolProgram is a real, decoded PoolProgram?v=2 response captured on 2026-10-03.
//
//go:embed poolprogram.json
var PoolProgram []byte
