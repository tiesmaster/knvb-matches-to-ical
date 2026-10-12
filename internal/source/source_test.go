package source

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/fixtures"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink/sportlinktest"
)

func live(srv *sportlinktest.Server) *Live {
	return &Live{API: srv.Client(), TeamID: "T1779014083", Logger: slog.New(slog.DiscardHandler)}
}

func TestLiveFlow(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	got, err := live(srv).Fixtures(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("team + pool programme must be de-duplicated: got %d fixtures", len(got))
	}
	if got[0].Facility == nil || got[0].Facility.FacilityName != "Sportpark VOC" {
		t.Errorf("facility enrichment missing: %+v", got[0].Facility)
	}
	want := []string{
		"team/TeamCompetitionData?v=2&PublicTeamId=T1779014083",
		"team/TeamProgram?v=3&PublicTeamId=T1779014083",
		"pool/PoolProgram?v=2&PoolId=839615",
		"match/MatchFacility?v=3&PublicMatchId=M707488589",
	}
	if r := srv.Requests(); !slices.Equal(r, want) {
		t.Errorf("requests:\n%s", strings.Join(r, "\n"))
	}
}

func TestLiveNoPoolsUsesTeamProgram(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	srv.Handle("team/TeamCompetitionData", sportlinktest.Body([]byte(`{"Pool": []}`)))
	got, err := live(srv).Fixtures(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("got %d, %v", len(got), err)
	}
	for _, r := range srv.Requests() {
		if strings.HasPrefix(r, "pool/") {
			t.Errorf("unexpected pool request %s", r)
		}
	}
}

func TestLiveIncludePast(t *testing.T) {
	srv := sportlinktest.NewServer(t)
	srv.Handle("team/TeamLatestResults", sportlinktest.Body([]byte(`{"Results": [
	  {"PublicMatchId": "M1", "MatchDateTime": "2026-09-27T09:00:00+0200", "Status": "Gespeeld",
	   "HomeTeam": {"PublicTeamId": "T1779014083", "TeamName": "Neptunus-Schiebroek O8-1"},
	   "AwayTeam": {"PublicTeamId": "T2", "TeamName": "X O8-1"}},
	  {"PublicMatchId": "M707488589", "MatchDateTime": "2026-10-03T08:30:00+0200"}
	]}`)))
	l := live(srv)
	l.IncludePast = true
	got, err := l.Fixtures(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range got {
		ids = append(ids, f.Item.Match.PublicMatchID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"M1", "M707488589"}) {
		t.Errorf("ids = %v", ids)
	}
}

func TestLiveFailuresAbort(t *testing.T) {
	for _, endpoint := range []string{"team/TeamCompetitionData", "team/TeamProgram", "pool/PoolProgram", "match/MatchFacility"} {
		t.Run(endpoint, func(t *testing.T) {
			srv := sportlinktest.NewServer(t)
			srv.Handle(endpoint, sportlinktest.Status(http.StatusInternalServerError, `boom`))
			if got, err := live(srv).Fixtures(context.Background()); err == nil {
				t.Fatalf("want error, got %d fixtures", len(got))
			}
		})
	}
}

func TestDummy(t *testing.T) {
	got, err := Dummy{}.Fixtures(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d fixtures, want 4", len(got))
	}
	byID := map[string]sportlink.Fixture{}
	for _, f := range got {
		byID[f.Item.Match.PublicMatchID] = f
	}
	if byID[DummyCancelledID].Item.Match.Status != "Afgelast" {
		t.Error("cancelled dummy match missing")
	}
	moved, _ := Dummy{Reschedule: true}.Fixtures(context.Background())
	for _, f := range moved {
		if f.Item.Match.PublicMatchID == DummyRescheduledID && f.Start().Equal(byID[DummyRescheduledID].Start()) {
			t.Error("Reschedule must move the rescheduled match")
		}
	}
	if len(fixtures.PoolProgram) == 0 {
		t.Error("embedded fixture is empty")
	}
}

func TestOwnTeamOnly(t *testing.T) {
	all, _ := Dummy{}.Fixtures(context.Background())
	own := OwnTeamOnly(all, "T1779014083")
	if len(own) != 3 {
		t.Errorf("got %d own fixtures, want 3 (the real pool match doesn't involve O8-1)", len(own))
	}
}
