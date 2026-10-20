package sportlink

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ProgramResponse is the shape shared by TeamProgram?v=3 and PoolProgram?v=2.
// Pointer slices distinguish a missing key (unexpected shape) from an empty list.
type ProgramResponse struct {
	ProgramItemMatch              *[]ProgramItemMatch `json:"ProgramItemMatch"`
	ProgramItemMatchSportlinkClub *[]ProgramItemMatch `json:"ProgramItemMatchSportlinkClub"`
	ProgramItemTournamentMatch    *[]ProgramItemMatch `json:"ProgramItemTournamentMatch"`
}

// ProgramItemMatch is one programme entry wrapping a Match.
type ProgramItemMatch struct {
	DetailsKey   string `json:"DetailsKey"`
	Timestamp    Time   `json:"Timestamp"`
	EndTimestamp Time   `json:"EndTimestamp"`
	Match        *Match `json:"Match"`
}

// Match is a single fixture.
type Match struct {
	PublicMatchID    string `json:"PublicMatchId"`
	ExternalMatchID  Text   `json:"ExternalMatchId"`
	MatchDateTime    Time   `json:"MatchDateTime"`
	Status           string `json:"Status"`
	DisciplineStatus string `json:"DisciplineStatus"`
	Location         string `json:"Location"`
	Pool             *Pool  `json:"Pool"`
	HomeTeam         Team   `json:"HomeTeam"`
	AwayTeam         Team   `json:"AwayTeam"`
}

// Team is a home or away team.
type Team struct {
	PublicTeamID string `json:"PublicTeamId"`
	TeamCode     string `json:"TeamCode"`
	TeamName     string `json:"TeamName"`
	Club         Club   `json:"Club"`
}

// Club is the club a team belongs to.
type Club struct {
	ClubID   string `json:"ClubId"`
	ClubName string `json:"ClubName"`
	City     string `json:"City"`
}

// Pool is the competition pool a match belongs to.
type Pool struct {
	PoolID          int64  `json:"PoolId"`
	PoolName        string `json:"PoolName"`
	ClassName       string `json:"ClassName"`
	CompetitionKind string `json:"CompetitionKind"`
}

// MatchFacility is the MatchFacility?v=3 response, a root-level object whose
// fields are strings or null (captured 2026-10-20). AssemblyTime and
// DepartureTime were null in every capture, so their format is unconfirmed.
type MatchFacility struct {
	FacilityID           string `json:"FacilityId"` // e.g. "BBDX37J"
	FacilityName         string `json:"FacilityName"`
	Address              string `json:"Address"`
	ZipCode              string `json:"ZipCode"`
	City                 string `json:"City"`
	PhoneNumber          string `json:"PhoneNumber"`
	SubFacilityName      string `json:"SubFacilityName"` // field, e.g. "veld 1 A1"
	FieldType            string `json:"FieldType"`
	AssemblyTime         string `json:"AssemblyTime"`
	DepartureTime        string `json:"DepartureTime"`
	HomeDressingRoom     string `json:"HomeDressingRoom"`
	AwayDressingRoom     string `json:"AwayDressingRoom"`
	OfficialDressingRoom string `json:"OfficialDressingRoom"`
	ClubInformationURL   string `json:"ClubInformationUrl"`
}

// Text decodes a JSON string, number or null into a string (ExternalMatchId
// is a JSON number).
type Text string

func (t *Text) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case bytes.Equal(b, []byte("null")):
		*t = ""
	case len(b) > 0 && b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*t = Text(s)
	case len(b) > 0 && (b[0] == '{' || b[0] == '['):
		return fmt.Errorf("expected scalar, got %.20s", b)
	default:
		*t = Text(b) // number or boolean literal, verbatim
	}
	return nil
}

// ErrUnexpectedShape marks a response whose structure doesn't match the contract.
var ErrUnexpectedShape = errors.New("unexpected response shape")

// DecodeProgram decodes a TeamProgram/PoolProgram body. It fails if the
// ProgramItemMatch array is missing, or if any match lacks an id or kickoff time,
// so a changed API never silently produces an empty calendar.
func DecodeProgram(body []byte) ([]ProgramItemMatch, error) {
	var r ProgramResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnexpectedShape, err)
	}
	if r.ProgramItemMatch == nil {
		return nil, fmt.Errorf("%w: top-level ProgramItemMatch array missing", ErrUnexpectedShape)
	}
	var items []ProgramItemMatch
	for _, list := range []*[]ProgramItemMatch{r.ProgramItemMatch, r.ProgramItemMatchSportlinkClub, r.ProgramItemTournamentMatch} {
		if list != nil {
			items = append(items, *list...)
		}
	}
	for i, it := range items {
		if err := validateItem(it); err != nil {
			return nil, fmt.Errorf("%w: item %d: %v", ErrUnexpectedShape, i, err)
		}
	}
	return items, nil
}

func validateItem(it ProgramItemMatch) error {
	switch {
	case it.Match == nil:
		return errors.New("Match missing")
	case it.Match.PublicMatchID == "":
		return errors.New("Match.PublicMatchId missing")
	case it.Match.MatchDateTime.IsZero() && it.Timestamp.IsZero():
		return fmt.Errorf("match %s: no MatchDateTime or Timestamp", it.Match.PublicMatchID)
	}
	return nil
}

// DecodeMatchFacility decodes a MatchFacility body.
func DecodeMatchFacility(body []byte) (*MatchFacility, error) {
	if b := bytes.TrimSpace(body); len(b) == 0 || b[0] != '{' {
		return nil, fmt.Errorf("%w: MatchFacility: expected a JSON object", ErrUnexpectedShape)
	}
	var f MatchFacility
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("%w: MatchFacility: %v", ErrUnexpectedShape, err)
	}
	return &f, nil
}

// DecodePoolIDs returns the PoolIds from a TeamCompetitionData body. Only the
// top-level Pool array is read: the *MatchResults siblings may reference other
// pools. A missing Pool array is an error; an empty one is not.
func DecodePoolIDs(body []byte) ([]int64, error) {
	var r struct {
		Pool *[]Pool `json:"Pool"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("%w: TeamCompetitionData: %v", ErrUnexpectedShape, err)
	}
	if r.Pool == nil {
		return nil, fmt.Errorf("%w: TeamCompetitionData: top-level Pool array missing", ErrUnexpectedShape)
	}
	var ids []int64
	for i, p := range *r.Pool {
		if p.PoolID <= 0 {
			return nil, fmt.Errorf("%w: TeamCompetitionData: Pool[%d] has no PoolId", ErrUnexpectedShape, i)
		}
		if !slices.Contains(ids, p.PoolID) {
			ids = append(ids, p.PoolID)
		}
	}
	return ids, nil
}

// DecodeLatestResults decodes a TeamLatestResults body,
// {"PublicTeamId": "…", "LatestResults": [...]}. A missing LatestResults key is
// an error. No non-empty capture exists yet, so the entries are scanned for
// Match-shaped objects (having PublicMatchId and MatchDateTime) at any depth.
func DecodeLatestResults(body []byte) ([]Match, error) {
	var r struct {
		LatestResults *json.RawMessage `json:"LatestResults"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("%w: TeamLatestResults: %v", ErrUnexpectedShape, err)
	}
	if r.LatestResults == nil {
		return nil, fmt.Errorf("%w: TeamLatestResults: LatestResults missing", ErrUnexpectedShape)
	}
	var root any
	if err := json.Unmarshal(*r.LatestResults, &root); err != nil {
		return nil, fmt.Errorf("%w: TeamLatestResults: %v", ErrUnexpectedShape, err)
	}
	var out []Match
	var errs []error
	walk(root, func(obj map[string]any) {
		if _, ok := obj["PublicMatchId"]; !ok {
			return
		}
		if _, ok := obj["MatchDateTime"]; !ok {
			return
		}
		raw, _ := json.Marshal(obj)
		var m Match
		if err := json.Unmarshal(raw, &m); err != nil {
			errs = append(errs, err)
			return
		}
		out = append(out, m)
	})
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%w: TeamLatestResults: %v", ErrUnexpectedShape, err)
	}
	return out, nil
}

func walk(v any, visit func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		visit(x)
		for _, c := range x {
			walk(c, visit)
		}
	case []any:
		for _, c := range x {
			walk(c, visit)
		}
	}
}

// Fixture is one programme item, optionally enriched with its MatchFacility.
type Fixture struct {
	Item     ProgramItemMatch
	Facility *MatchFacility
}

// Start is the kickoff: MatchDateTime, falling back to the programme Timestamp.
func (f Fixture) Start() time.Time {
	if !f.Item.Match.MatchDateTime.IsZero() {
		return f.Item.Match.MatchDateTime.Time
	}
	return f.Item.Timestamp.Time
}
