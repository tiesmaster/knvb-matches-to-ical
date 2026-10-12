// Package state tracks per-event content hashes, SEQUENCE and LAST-MODIFIED
// between runs, and decides what changed.
package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/calendar"
)

// Version is the state.json schema version.
const Version = 1

// VanishedSuffix is appended to SUMMARY when an upcoming match disappears from
// the programme without an explicit cancellation.
const VanishedSuffix = " (vervallen)"

// State is persisted as state.json next to the feed.
type State struct {
	Version int              `json:"version"`
	Events  map[string]Entry `json:"events"`
}

// Entry is the stored revision of one event.
type Entry struct {
	Hash         string         `json:"hash"`
	Sequence     int            `json:"sequence"`
	LastModified time.Time      `json:"last_modified"`
	Event        calendar.Event `json:"event"`
	// Vanished is set when the event is no longer returned by the API and is
	// only kept in the feed from state (as a past match or a cancellation).
	Vanished bool `json:"vanished,omitempty"`
}

// New returns an empty state.
func New() State { return State{Version: Version, Events: map[string]Entry{}} }

// Parse decodes state.json. Empty input yields an empty state.
func Parse(b []byte) (State, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return New(), nil
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("parse state: %w", err)
	}
	if s.Version != Version {
		return State{}, fmt.Errorf("parse state: unsupported version %d (want %d)", s.Version, Version)
	}
	if s.Events == nil {
		s.Events = map[string]Entry{}
	}
	return s, nil
}

// Marshal encodes the state deterministically (map keys are sorted).
func (s State) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// HasUpcoming reports whether the state holds an event, still present in the
// API at the last run, that starts after now.
func (s State) HasUpcoming(now time.Time) bool {
	for _, e := range s.Events {
		if !e.Vanished && e.Event.Start.After(now) {
			return true
		}
	}
	return false
}

// Items returns the feed contents in a stable order.
func (s State) Items() []calendar.Item {
	items := make([]calendar.Item, 0, len(s.Events))
	for _, uid := range slices.Sorted(maps.Keys(s.Events)) {
		e := s.Events[uid]
		items = append(items, calendar.Item{Event: e.Event, Sequence: e.Sequence, LastModified: e.LastModified})
	}
	return items
}

// ChangeKind describes what happened to a UID in a run.
type ChangeKind string

const (
	Added    ChangeKind = "added"
	Updated  ChangeKind = "updated"
	Vanished ChangeKind = "vanished" // upcoming match no longer in the API → CANCELLED
	Pruned   ChangeKind = "pruned"   // retention expired → removed from the feed
)

// Change is one per-UID difference between the previous and next state.
type Change struct {
	UID      string
	Kind     ChangeKind
	Old      *calendar.Event // nil for Added
	New      *calendar.Event // nil for Pruned
	Sequence int
}

func (c Change) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-8s %s seq=%d", c.Kind, c.UID, c.Sequence)
	switch {
	case c.New != nil && c.Old == nil:
		fmt.Fprintf(&b, " %s %q", c.New.Start.Format(time.RFC3339), c.New.Summary)
	case c.New != nil:
		for _, f := range FieldsChanged(*c.Old, *c.New) {
			fmt.Fprintf(&b, " %s", f)
		}
	case c.Old != nil:
		fmt.Fprintf(&b, " %s %q", c.Old.Start.Format(time.RFC3339), c.Old.Summary)
	}
	return b.String()
}

// FieldsChanged lists the names of the fields that differ between a and b.
func FieldsChanged(a, b calendar.Event) []string {
	var out []string
	if !a.Start.Equal(b.Start) {
		out = append(out, "start")
	}
	if !a.End.Equal(b.End) {
		out = append(out, "end")
	}
	if a.Summary != b.Summary {
		out = append(out, "summary")
	}
	if a.Location != b.Location {
		out = append(out, "location")
	}
	if a.Description != b.Description {
		out = append(out, "description")
	}
	if a.Status != b.Status {
		out = append(out, "status")
	}
	return out
}

// Result is the outcome of Diff.
type Result struct {
	Next    State
	Changes []Change
}

// Changed reports whether the feed must be republished.
func (r Result) Changed() bool { return len(r.Changes) > 0 }

// Diff compares the events fetched this run against the previous state.
//
//   - New UID: SEQUENCE 0, LAST-MODIFIED now.
//   - Hash differs: SEQUENCE+1, LAST-MODIFIED now.
//   - UID no longer returned, match already started: kept unchanged (the API
//     only lists upcoming matches, and played ones should stay in the calendar).
//   - UID no longer returned, match still upcoming: kept once more as
//     STATUS:CANCELLED with SEQUENCE+1.
//   - Entries not returned by the API are pruned once their end is more than
//     retain ago.
//
// Duplicate UIDs in current keep the first occurrence.
func Diff(prev State, current []calendar.Event, now time.Time, retain time.Duration) Result {
	now = now.UTC()
	next := New()
	var changes []Change

	for _, ev := range current {
		if _, dup := next.Events[ev.UID]; dup {
			continue
		}
		h := ev.Hash()
		old, existed := prev.Events[ev.UID]
		switch {
		case !existed:
			next.Events[ev.UID] = Entry{Hash: h, Sequence: 0, LastModified: now, Event: ev}
			changes = append(changes, Change{UID: ev.UID, Kind: Added, New: ptr(ev)})
		case old.Hash != h:
			seq := old.Sequence + 1
			next.Events[ev.UID] = Entry{Hash: h, Sequence: seq, LastModified: now, Event: ev}
			changes = append(changes, Change{UID: ev.UID, Kind: Updated, Old: ptr(old.Event), New: ptr(ev), Sequence: seq})
		default:
			old.Vanished = false
			next.Events[ev.UID] = old
		}
	}

	for _, uid := range slices.Sorted(maps.Keys(prev.Events)) {
		if _, ok := next.Events[uid]; ok {
			continue
		}
		old := prev.Events[uid]
		switch {
		case old.Event.End.Add(retain).Before(now):
			changes = append(changes, Change{UID: uid, Kind: Pruned, Old: ptr(old.Event), Sequence: old.Sequence})
		case old.Vanished || !old.Event.Start.After(now):
			old.Vanished = true
			next.Events[uid] = old
		default:
			ev := old.Event
			ev.Status = calendar.ICalCancelled
			if !strings.HasSuffix(ev.Summary, VanishedSuffix) {
				ev.Summary += VanishedSuffix
			}
			seq := old.Sequence + 1
			next.Events[uid] = Entry{Hash: ev.Hash(), Sequence: seq, LastModified: now, Event: ev, Vanished: true}
			changes = append(changes, Change{UID: uid, Kind: Vanished, Old: ptr(old.Event), New: ptr(ev), Sequence: seq})
		}
	}

	slices.SortFunc(changes, func(a, b Change) int { return strings.Compare(a.UID, b.UID) })
	return Result{Next: next, Changes: changes}
}

func ptr[T any](v T) *T { return &v }
