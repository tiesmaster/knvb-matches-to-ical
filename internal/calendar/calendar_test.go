package calendar

import (
	"bytes"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/fixtures"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestClassifyStatus(t *testing.T) {
	for raw, want := range map[string]struct {
		kind StatusKind
		ical string
	}{
		"":             {StatusNormal, ICalConfirmed},
		"SCHEDULED":    {StatusNormal, ICalConfirmed},
		"Gespeeld":     {StatusNormal, ICalConfirmed},
		"Afgelast":     {StatusCancelled, ICalCancelled},
		"AFGELAST":     {StatusCancelled, ICalCancelled},
		" afgelast ":   {StatusCancelled, ICalCancelled},
		"CANCELLED":    {StatusCancelled, ICalCancelled},
		"Gestaakt":     {StatusAbandoned, ICalCancelled},
		"Uitgesteld":   {StatusPostponed, ICalTentative},
		"Verplaatst":   {StatusPostponed, ICalTentative},
		"POSTPONED":    {StatusPostponed, ICalTentative},
		"Onbekend123":  {StatusUnknown, ICalConfirmed},
		"IN_PROGRESS?": {StatusUnknown, ICalConfirmed},
	} {
		k := ClassifyStatus(raw)
		if k != want.kind || k.ICalStatus() != want.ical {
			t.Errorf("%q: got (%v, %s), want (%v, %s)", raw, k, k.ICalStatus(), want.kind, want.ical)
		}
	}
}

func fixtureFromJSON(t *testing.T) sportlink.Fixture {
	t.Helper()
	items, err := sportlink.DecodeProgram(fixtures.PoolProgram)
	if err != nil {
		t.Fatal(err)
	}
	return sportlink.Fixture{Item: items[0]}
}

var opts = MapOptions{DefaultDuration: time.Hour, TeamID: "T1779014083"}

func TestToEventFromRealFixture(t *testing.T) {
	e := ToEvent(fixtureFromJSON(t), opts)
	want := Event{
		UID:         "M707488589@voetbal.nl",
		Start:       time.Date(2026, 10, 3, 6, 30, 0, 0, time.UTC),
		End:         time.Date(2026, 10, 3, 7, 30, 0, 0, time.UTC),
		Summary:     "VOC O8-5 – BVCB O8-4",
		Description: "Competitie: 4e klasse Zwaluwen Jeugd (1e fase), poule 12\nWedstrijdnummer: 36736",
		Status:      ICalConfirmed,
	}
	if e != want {
		t.Errorf("got  %+v\nwant %+v", e, want)
	}
}

func TestToEventWithFacilityAndStatus(t *testing.T) {
	f := fixtureFromJSON(t)
	f.Item.Match.Status = "Afgelast"
	f.Item.Match.Location = "Sportpark VOC"
	f.Item.EndTimestamp = sportlink.Time{Time: time.Date(2026, 10, 3, 7, 15, 0, 0, time.UTC)}
	f.Facility = &sportlink.MatchFacility{
		FacilityName:     "Sportpark VOC",
		SubFacilityName:  "Veld 3",
		Address:          "Kralingse Plaslaan 1",
		ZipCode:          "3062 AA",
		City:             "Rotterdam",
		AssemblyTime:     "2026-10-03T08:00:00+0200",
		DepartureTime:    "07:30",
		HomeDressingRoom: "4",
		FieldType:        "Kunstgras",
	}
	e := ToEvent(f, opts)
	if e.Status != ICalCancelled || e.Summary != "VOC O8-5 – BVCB O8-4 (afgelast)" {
		t.Errorf("status/summary: %q %q", e.Status, e.Summary)
	}
	if e.Location != "Sportpark VOC (Veld 3), Kralingse Plaslaan 1, 3062 AA Rotterdam" {
		t.Errorf("location: %q", e.Location)
	}
	if !e.End.Equal(time.Date(2026, 10, 3, 7, 15, 0, 0, time.UTC)) {
		t.Errorf("end should come from EndTimestamp: %s", e.End)
	}
	for _, line := range []string{"Status: Afgelast", "Verzamelen: 08:00", "Vertrek: 07:30", "Kleedkamer thuis: 4", "Veldtype: Kunstgras", "Wedstrijdnummer: 36736"} {
		if !strings.Contains(e.Description, line) {
			t.Errorf("description missing %q:\n%s", line, e.Description)
		}
	}
}

func TestToEventFallbacks(t *testing.T) {
	f := fixtureFromJSON(t)
	f.Item.Match.Location = "Sportpark VOC"
	f.Facility = &sportlink.MatchFacility{} // empty facility → programme Location
	f.Item.Match.MatchDateTime = sportlink.Time{}
	f.Item.Match.HomeTeam.TeamName = ""
	var logs bytes.Buffer
	f.Item.Match.Status = "Raar"
	e := ToEvent(f, MapOptions{DefaultDuration: 50 * time.Minute, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	if e.Location != "Sportpark VOC" {
		t.Errorf("location: %q", e.Location)
	}
	if !e.Start.Equal(time.Date(2026, 10, 3, 6, 30, 0, 0, time.UTC)) || e.End.Sub(e.Start) != 50*time.Minute {
		t.Errorf("start/end from Timestamp + default duration: %s–%s", e.Start, e.End)
	}
	if e.Summary != "VOC O8-5 – BVCB O8-4" {
		t.Errorf("summary should fall back to club+code: %q", e.Summary)
	}
	if e.Status != ICalConfirmed || !strings.Contains(logs.String(), `"level":"WARN"`) || !strings.Contains(logs.String(), "Raar") {
		t.Errorf("unknown status should be CONFIRMED and warned: %s / %s", e.Status, logs.String())
	}
}

func TestSummaryHomeAway(t *testing.T) {
	us := sportlink.Team{PublicTeamID: "T1779014083", TeamName: "Neptunus-Schiebroek O8-1"}
	for name, tc := range map[string]struct {
		home, away sportlink.Team
		status     string
		want       string
	}{
		"home":           {us, sportlink.Team{PublicTeamID: "T2", TeamName: "BVCB O8-3"}, "SCHEDULED", "THUIS: BVCB O8-3"},
		"away":           {sportlink.Team{PublicTeamID: "T3", TeamName: "VOC O8-5"}, us, "SCHEDULED", "UIT: VOC O8-5"},
		"away cancelled": {sportlink.Team{PublicTeamID: "T3", TeamName: "VOC O8-5"}, us, "Afgelast", "UIT: VOC O8-5 (afgelast)"},
		"home postponed": {us, sportlink.Team{PublicTeamID: "T2", TeamName: "BVCB O8-3"}, "Uitgesteld", "THUIS: BVCB O8-3 (uitgesteld)"},
		"not ours":       {sportlink.Team{PublicTeamID: "T3", TeamName: "VOC O8-5"}, sportlink.Team{PublicTeamID: "T2", TeamName: "BVCB O8-3"}, "", "VOC O8-5 – BVCB O8-3"},
	} {
		f := fixtureFromJSON(t)
		f.Item.Match.HomeTeam, f.Item.Match.AwayTeam, f.Item.Match.Status = tc.home, tc.away, tc.status
		if got := ToEvent(f, opts).Summary; got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
	// Without a TeamID both teams are named.
	f := fixtureFromJSON(t)
	f.Item.Match.HomeTeam = us
	if got := ToEvent(f, MapOptions{DefaultDuration: time.Hour}).Summary; got != "Neptunus-Schiebroek O8-1 – BVCB O8-4" {
		t.Errorf("no TeamID: %q", got)
	}
}

func TestHashIgnoresTimeZoneRepresentation(t *testing.T) {
	e := ToEvent(fixtureFromJSON(t), opts)
	e2 := e
	e2.Start = e.Start.In(sportlink.Amsterdam())
	if e.Hash() != e2.Hash() {
		t.Error("hash must not depend on time.Location")
	}
	e2.Summary += "!"
	if e.Hash() == e2.Hash() {
		t.Error("hash must change with content")
	}
}

func TestRenderGolden(t *testing.T) {
	real := ToEvent(fixtureFromJSON(t), opts)
	modified := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	items := []Item{
		{Event: Event{
			UID:     "M900000002@voetbal.nl",
			Start:   time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC),
			End:     time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
			Summary: "THUIS: VOC O8-5 (afgelast)",
			Location: "Sportpark Neptunus-Schiebroek (Veld 2), Abraham van Stolkweg 31, 3041 JA Rotterdam" +
				" — a long line to exercise folding",
			Description: "Status: Afgelast\nVerzamelen: 09:30\nKleedkamer thuis: 3; uit: 4\nWedstrijdnummer: 40002",
			Status:      ICalCancelled,
		}, Sequence: 2, LastModified: modified},
		{Event: real, Sequence: 0, LastModified: modified},
	}
	got := Render(Feed{Name: "Neptunus-Schiebroek O8-1"}, items)

	golden := filepath.Join("testdata", "feed.golden.ics")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("render mismatch (run with -update to accept)\n--- got ---\n%s", got)
	}

	s := string(got)
	for _, line := range strings.Split(strings.TrimSuffix(s, "\r\n"), "\r\n") {
		if len(line) > 75 {
			t.Errorf("line longer than 75 octets: %q", line)
		}
		if !utf8.ValidString(line) {
			t.Errorf("folding split a multi-byte character: %q", line)
		}
	}
	if strings.Index(s, "M707488589@voetbal.nl") > strings.Index(s, "M900000002@voetbal.nl") {
		t.Error("events must be sorted by start time")
	}
	if !bytes.Equal(Render(Feed{Name: "Neptunus-Schiebroek O8-1"}, items), got) {
		t.Error("render must be deterministic")
	}
}
