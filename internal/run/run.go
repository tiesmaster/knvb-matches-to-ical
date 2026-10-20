// Package run wires one fetch → map → diff → publish → notify cycle.
package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/calendar"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/source"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/state"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/store"
)

const (
	feedContentType  = "text/calendar; charset=utf-8"
	feedCacheControl = "max-age=300"
	stateContentType = "application/json"
	stateCacheCtl    = "no-store"
)

// ErrEmptyFeed is returned when the fetch yields no events although the
// previous state had upcoming ones (and ALLOW_EMPTY is not set).
var ErrEmptyFeed = errors.New("refusing to publish: no events fetched but previous state has upcoming matches (set ALLOW_EMPTY=1 to override)")

// Notifier is told about the changes of a published run.
type Notifier interface {
	Notify(ctx context.Context, now time.Time, changes []state.Change) error
}

// Options configure a run.
type Options struct {
	FeedKey         string
	StateKey        string
	CalName         string
	TeamID          string
	OnlyOwnTeam     bool
	DefaultDuration time.Duration
	Retain          time.Duration
	AllowEmpty      bool
}

// Runner holds a run's dependencies.
type Runner struct {
	Source   source.FixtureSource
	Store    store.Store
	Notifier Notifier // optional
	Now      func() time.Time
	Logger   *slog.Logger
	Options  Options
}

// Result summarises a run.
type Result struct {
	Published bool
	Events    int // events in the published feed (fetched + retained)
	Fetched   int
	Changes   []state.Change
}

// Run executes one cycle. On any error before publishing, nothing is written.
func (r *Runner) Run(ctx context.Context) (Result, error) {
	now := r.Now().UTC()
	o := r.Options

	fixtures, err := r.Source.Fixtures(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("fetch fixtures: %w", err)
	}
	if o.OnlyOwnTeam {
		n := len(fixtures)
		fixtures = source.OwnTeamOnly(fixtures, o.TeamID)
		r.Logger.Info("filtered to own team", "kept", len(fixtures), "dropped", n-len(fixtures))
	}
	events := make([]calendar.Event, 0, len(fixtures))
	for _, f := range fixtures {
		events = append(events, calendar.ToEvent(f, calendar.MapOptions{DefaultDuration: o.DefaultDuration, TeamID: o.TeamID, Logger: r.Logger}))
	}

	raw, found, err := r.Store.Get(ctx, o.StateKey)
	if err != nil {
		return Result{}, fmt.Errorf("load state: %w", err)
	}
	prev, err := state.Parse(raw)
	if err != nil {
		return Result{}, fmt.Errorf("load state %s: %w", o.StateKey, err)
	}
	if !found {
		r.Logger.Info("no previous state; starting empty", "key", o.StateKey)
	}

	if len(events) == 0 && prev.HasUpcoming(now) {
		if !o.AllowEmpty {
			return Result{}, ErrEmptyFeed
		}
		r.Logger.Warn("no events fetched but previous state had upcoming matches; ALLOW_EMPTY set, continuing")
	}

	diff := state.Diff(prev, events, now, o.Retain)
	res := Result{Events: len(diff.Next.Events), Fetched: len(events), Changes: diff.Changes}
	for _, c := range diff.Changes {
		r.Logger.Info("change", "uid", c.UID, "kind", c.Kind, "sequence", c.Sequence, "detail", c.String())
	}

	// Publish on the first run even without events, so the URL exists.
	if !diff.Changed() && found {
		r.Logger.Info("no changes; nothing uploaded", "events", res.Events)
		return res, nil
	}

	ics := calendar.Render(calendar.Feed{Name: o.CalName}, diff.Next.Items())
	stateJSON, err := diff.Next.Marshal()
	if err != nil {
		return res, fmt.Errorf("encode state: %w", err)
	}
	// Feed first: if the state upload then fails, the next run recomputes the
	// same revisions (with a newer LAST-MODIFIED) and republishes.
	if err := r.Store.Put(ctx, o.FeedKey, ics, feedContentType, feedCacheControl); err != nil {
		return res, fmt.Errorf("upload feed: %w", err)
	}
	if err := r.Store.Put(ctx, o.StateKey, stateJSON, stateContentType, stateCacheCtl); err != nil {
		return res, fmt.Errorf("upload state: %w", err)
	}
	res.Published = true
	r.Logger.Info("published", "feed", o.FeedKey, "events", res.Events, "changes", len(diff.Changes), "bytes", len(ics))

	if r.Notifier != nil && len(diff.Changes) > 0 {
		if err := r.Notifier.Notify(ctx, now, diff.Changes); err != nil {
			// The feed is already published; a failed notification shouldn't fail the job.
			r.Logger.Warn("notification failed", "err", err)
		}
	}
	return res, nil
}
