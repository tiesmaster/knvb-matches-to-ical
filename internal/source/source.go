// Package source provides fixture sources: the live Sportlink API and an
// embedded dummy dataset for development.
package source

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink"
)

// FixtureSource yields the fixtures to publish.
type FixtureSource interface {
	Fixtures(ctx context.Context) ([]sportlink.Fixture, error)
}

// API is the subset of *sportlink.Client used by Live.
type API interface {
	TeamCompetitionData(ctx context.Context, teamID string) ([]int64, error)
	TeamProgram(ctx context.Context, teamID string) ([]sportlink.ProgramItemMatch, error)
	PoolProgram(ctx context.Context, poolID int64) ([]sportlink.ProgramItemMatch, error)
	MatchFacility(ctx context.Context, matchID string) (*sportlink.MatchFacility, error)
	TeamLatestResults(ctx context.Context, teamID string) ([]sportlink.Match, error)
}

// Live implements the REQUIREMENTS §11.2 flow.
type Live struct {
	API         API
	TeamID      string
	IncludePast bool
	Logger      *slog.Logger
}

// Fixtures fetches TeamCompetitionData → PoolProgram per pool, merged with
// TeamProgram (which covers non-pool teams and non-competition matches),
// optionally TeamLatestResults, then MatchFacility per match. Any failure
// aborts the run so a partial result is never published.
func (l *Live) Fixtures(ctx context.Context) ([]sportlink.Fixture, error) {
	poolIDs, err := l.API.TeamCompetitionData(ctx, l.TeamID)
	if err != nil {
		return nil, fmt.Errorf("team competition data: %w", err)
	}
	l.Logger.Info("resolved pools", "team", l.TeamID, "pools", poolIDs)

	var m merger
	team, err := l.API.TeamProgram(ctx, l.TeamID)
	if err != nil {
		return nil, fmt.Errorf("team program: %w", err)
	}
	m.add(team...)
	for _, id := range poolIDs {
		items, err := l.API.PoolProgram(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("pool %d program: %w", id, err)
		}
		l.Logger.Info("fetched pool program", "pool", id, "matches", len(items))
		m.add(items...)
	}
	if l.IncludePast {
		past, err := l.API.TeamLatestResults(ctx, l.TeamID)
		if err != nil {
			return nil, fmt.Errorf("team latest results: %w", err)
		}
		for _, match := range past {
			if match.PublicMatchID == "" || match.MatchDateTime.IsZero() {
				continue
			}
			m.add(sportlink.ProgramItemMatch{DetailsKey: match.PublicMatchID, Timestamp: match.MatchDateTime, Match: &match})
		}
	}

	fixtures := make([]sportlink.Fixture, 0, len(m.items))
	for _, it := range m.items {
		fac, err := l.API.MatchFacility(ctx, it.Match.PublicMatchID)
		if err != nil {
			return nil, fmt.Errorf("match facility %s: %w", it.Match.PublicMatchID, err)
		}
		fixtures = append(fixtures, sportlink.Fixture{Item: it, Facility: fac})
	}
	l.Logger.Info("fetched fixtures", "team_program", len(team), "total", len(fixtures))
	return fixtures, nil
}

// merger collects programme items, keeping the first occurrence of each match.
type merger struct {
	seen  map[string]bool
	items []sportlink.ProgramItemMatch
}

func (m *merger) add(items ...sportlink.ProgramItemMatch) {
	if m.seen == nil {
		m.seen = map[string]bool{}
	}
	for _, it := range items {
		if id := it.Match.PublicMatchID; !m.seen[id] {
			m.seen[id] = true
			m.items = append(m.items, it)
		}
	}
}

// OwnTeamOnly keeps fixtures where our team is home or away.
func OwnTeamOnly(fixtures []sportlink.Fixture, teamID string) []sportlink.Fixture {
	var out []sportlink.Fixture
	for _, f := range fixtures {
		m := f.Item.Match
		if m.HomeTeam.PublicTeamID == teamID || m.AwayTeam.PublicTeamID == teamID {
			out = append(out, f)
		}
	}
	return out
}
