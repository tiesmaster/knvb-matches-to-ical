package sportlink

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
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

// MatchFacility is the MatchFacility?v=3 response.
//
// NOTE: the field list comes from the decompiled app (REQUIREMENTS §4.1); no live
// sample has been captured yet, so every field is decoded leniently as Text.
type MatchFacility struct {
	FacilityID           Text `json:"FacilityId"`
	FacilityName         Text `json:"FacilityName"`
	Address              Text `json:"Address"`
	ZipCode              Text `json:"ZipCode"`
	City                 Text `json:"City"`
	PhoneNumber          Text `json:"PhoneNumber"`
	SubFacilityName      Text `json:"SubFacilityName"`
	FieldType            Text `json:"FieldType"`
	AssemblyTime         Text `json:"AssemblyTime"`
	DepartureTime        Text `json:"DepartureTime"`
	HomeDressingRoom     Text `json:"HomeDressingRoom"`
	AwayDressingRoom     Text `json:"AwayDressingRoom"`
	OfficialDressingRoom Text `json:"OfficialDressingRoom"`
	ClubInformationURL   Text `json:"ClubInformationUrl"`
}

// Text decodes a JSON string, number, boolean or null into a string. It is used
// for fields whose JSON type has not been confirmed against a live response.
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

// DecodeMatchFacility decodes a MatchFacility body. It accepts the object either
// at the root or wrapped as {"MatchFacility": {...}}.
func DecodeMatchFacility(body []byte) (*MatchFacility, error) {
	var wrapped struct {
		MatchFacility *json.RawMessage `json:"MatchFacility"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnexpectedShape, err)
	}
	if wrapped.MatchFacility != nil {
		body = *wrapped.MatchFacility
	}
	var f MatchFacility
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnexpectedShape, err)
	}
	return &f, nil
}

// DecodePoolIDs extracts every PoolId from a TeamCompetitionData body.
//
// NOTE: the exact response shape hasn't been captured yet (REQUIREMENTS §11.2 only
// says "Pool[].PoolId"), so this walks the whole document for PoolId values.
func DecodePoolIDs(body []byte) ([]int64, error) {
	var root any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnexpectedShape, err)
	}
	if _, ok := root.(map[string]any); !ok {
		return nil, fmt.Errorf("%w: expected a JSON object", ErrUnexpectedShape)
	}
	seen := map[int64]bool{}
	var ids []int64
	walk(root, func(obj map[string]any) {
		var id int64
		switch v := obj["PoolId"].(type) {
		case json.Number:
			id, _ = v.Int64()
		case string:
			id, _ = strconv.ParseInt(v, 10, 64)
		}
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	})
	return ids, nil
}

// DecodeMatches extracts every Match-shaped object (having PublicMatchId and
// MatchDateTime) from a body of unconfirmed shape, such as TeamLatestResults.
func DecodeMatches(body []byte) ([]Match, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnexpectedShape, err)
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
		return nil, fmt.Errorf("%w: %v", ErrUnexpectedShape, err)
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
