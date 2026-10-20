// Package fixtures embeds real and synthetic Sportlink API responses, used by
// the dummy source and by tests.
package fixtures

import _ "embed"

// PoolProgram is a real PoolProgram?v=2 response captured on 2026-10-03.
//
//go:embed poolprogram.json
var PoolProgram []byte

// Real responses for O8-1 (T1779014083), captured on 2026-10-20.
var (
	// TeamCompetitionData lists the 1e and 2e fase pools.
	//
	//go:embed teamcompetitiondata.json
	TeamCompetitionData []byte

	// MatchFacilityHome is a home match on Sportpark Neptunus-Schiebroek (M1771286118).
	//
	//go:embed matchfacility-home.json
	MatchFacilityHome []byte

	// MatchFacilityAway is an away match on Sportpark VOC (M1481718857).
	//
	//go:embed matchfacility-away.json
	MatchFacilityAway []byte

	// TeamLatestResults is empty: no 2e fase match had been played yet.
	//
	//go:embed teamlatestresults.json
	TeamLatestResults []byte
)
