package sportlink

import (
	"errors"
	"testing"
	"time"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/fixtures"
)

func TestParseTime(t *testing.T) {
	want := time.Date(2026, 10, 3, 6, 30, 0, 0, time.UTC)
	for _, in := range []string{
		"2026-10-03T08:30:00+0200",
		"2026-10-03T08:30:00+02:00",
		"2026-10-03T06:30:00+0000",
		"2026-10-03T08:30:00", // no offset → Europe/Amsterdam (CEST)
		" 2026-10-03T08:30:00+0200 ",
	} {
		got, err := ParseTime(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("%q: got %s, want %s", in, got.UTC(), want)
		}
	}
	// Winter time: no offset → CET (+01:00).
	got, err := ParseTime("2026-12-05T09:00:00")
	if err != nil || !got.Equal(time.Date(2026, 12, 5, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("winter: got %s, %v", got.UTC(), err)
	}
	for _, bad := range []string{"", "03-10-2026 08:30", "2026-10-03"} {
		if _, err := ParseTime(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestDecodePoolProgramFixture(t *testing.T) {
	items, err := DecodeProgram(fixtures.PoolProgram)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	m := it.Match
	if it.DetailsKey != "M707488589" || !it.EndTimestamp.IsZero() {
		t.Errorf("item: %+v", it)
	}
	if m.PublicMatchID != "M707488589" || m.ExternalMatchID != "36736" || m.Status != "SCHEDULED" ||
		m.DisciplineStatus != "" || m.Location != "" {
		t.Errorf("match: %+v", m)
	}
	if !m.MatchDateTime.Equal(time.Date(2026, 10, 3, 6, 30, 0, 0, time.UTC)) {
		t.Errorf("MatchDateTime = %s", m.MatchDateTime.UTC())
	}
	if m.HomeTeam.PublicTeamID != "T1049108481" || m.HomeTeam.TeamName != "VOC O8-5" || m.HomeTeam.Club.City != "ROTTERDAM" {
		t.Errorf("home: %+v", m.HomeTeam)
	}
	if m.AwayTeam.TeamName != "BVCB O8-4" || m.AwayTeam.Club.ClubName != "BVCB" {
		t.Errorf("away: %+v", m.AwayTeam)
	}
	if m.Pool == nil || m.Pool.PoolID != 839615 || m.Pool.ClassName != "4e klasse Zwaluwen Jeugd (1e fase)" {
		t.Errorf("pool: %+v", m.Pool)
	}
}

func TestDecodeProgramShapeErrors(t *testing.T) {
	for name, body := range map[string]string{
		"not json":        `<html>`,
		"array root":      `[]`,
		"missing key":     `{"ProgramItemAnnouncement": []}`,
		"null key":        `{"ProgramItemMatch": null}`,
		"no match":        `{"ProgramItemMatch": [{"DetailsKey": "x"}]}`,
		"no id":           `{"ProgramItemMatch": [{"Match": {"MatchDateTime": "2026-10-03T08:30:00+0200"}}]}`,
		"no time":         `{"ProgramItemMatch": [{"Match": {"PublicMatchId": "M1"}}]}`,
		"bad time format": `{"ProgramItemMatch": [{"Match": {"PublicMatchId": "M1", "MatchDateTime": "3 okt"}}]}`,
	} {
		if _, err := DecodeProgram([]byte(body)); !errors.Is(err, ErrUnexpectedShape) {
			t.Errorf("%s: want ErrUnexpectedShape, got %v", name, err)
		}
	}
	items, err := DecodeProgram([]byte(`{"ProgramItemMatch": []}`))
	if err != nil || len(items) != 0 {
		t.Errorf("empty programme: %v, %v", items, err)
	}
}

func TestDecodeProgramMergesArrays(t *testing.T) {
	body := `{
	  "ProgramItemMatch": [{"Match": {"PublicMatchId": "M1", "MatchDateTime": "2026-10-03T08:30:00+0200"}}],
	  "ProgramItemMatchSportlinkClub": [{"Match": {"PublicMatchId": "M2", "MatchDateTime": "2026-10-04T08:30:00+0200"}}],
	  "ProgramItemTournamentMatch": [{"Match": {"PublicMatchId": "M3", "MatchDateTime": "2026-10-05T08:30:00+0200"}}]
	}`
	items, err := DecodeProgram([]byte(body))
	if err != nil || len(items) != 3 {
		t.Fatalf("got %d items, %v", len(items), err)
	}
}

func TestDecodeMatchFacility(t *testing.T) {
	for name, body := range map[string]string{
		"root":    `{"FacilityName": "Sportpark VOC", "Address": "Kralingse Plaslaan 1", "ZipCode": "3062 AA", "City": "Rotterdam", "AssemblyTime": "08:00", "FacilityId": 1234, "HomeDressingRoom": null}`,
		"wrapped": `{"MatchFacility": {"FacilityName": "Sportpark VOC", "Address": "Kralingse Plaslaan 1", "ZipCode": "3062 AA", "City": "Rotterdam", "AssemblyTime": "08:00", "FacilityId": 1234}}`,
	} {
		f, err := DecodeMatchFacility([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if f.FacilityName != "Sportpark VOC" || f.ZipCode != "3062 AA" || f.AssemblyTime != "08:00" || f.FacilityID != "1234" || f.HomeDressingRoom != "" {
			t.Errorf("%s: %+v", name, f)
		}
	}
	if _, err := DecodeMatchFacility([]byte(`{"FacilityName": {"nested": true}}`)); !errors.Is(err, ErrUnexpectedShape) {
		t.Errorf("want ErrUnexpectedShape, got %v", err)
	}
}

func TestDecodePoolIDs(t *testing.T) {
	ids, err := DecodePoolIDs([]byte(`{"Pool": [{"PoolId": 839615, "ClassName": "x"}, {"PoolId": "900001"}], "Other": {"Pool": {"PoolId": 839615}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Errorf("ids = %v", ids)
	}
	ids, err = DecodePoolIDs([]byte(`{"Competition": []}`))
	if err != nil || len(ids) != 0 {
		t.Errorf("no pools: %v %v", ids, err)
	}
	if _, err := DecodePoolIDs([]byte(`[1,2]`)); !errors.Is(err, ErrUnexpectedShape) {
		t.Errorf("want ErrUnexpectedShape, got %v", err)
	}
}

func TestDecodeMatches(t *testing.T) {
	ms, err := DecodeMatches(fixtures.PoolProgram)
	if err != nil || len(ms) != 1 || ms[0].PublicMatchID != "M707488589" {
		t.Fatalf("got %+v, %v", ms, err)
	}
}
