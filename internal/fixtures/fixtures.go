// Package fixtures embeds real and synthetic Sportlink API responses, used by
// the dummy source and by tests.
package fixtures

import _ "embed"

// PoolProgram is a real, decoded PoolProgram?v=2 response captured on 2026-10-03.
//
//go:embed poolprogram.json
var PoolProgram []byte

// CompetitionData is a SYNTHETIC TeamCompetitionData?v=2 response; the real
// shape has not been captured yet.
//
//go:embed competitiondata.synthetic.json
var CompetitionData []byte

// MatchFacility is a SYNTHETIC MatchFacility?v=3 response; the real shape has
// not been captured yet.
//
//go:embed matchfacility.synthetic.json
var MatchFacility []byte
