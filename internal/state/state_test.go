package state

import (
	"strings"
	"testing"
	"time"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/calendar"
)

var (
	t0     = time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC) // first run
	t1     = t0.Add(12 * time.Hour)                        // second run
	retain = 180 * 24 * time.Hour
)

func ev(id string, start time.Time) calendar.Event {
	return calendar.Event{
		UID: id + "@voetbal.nl", Start: start, End: start.Add(time.Hour),
		Summary: "A – B", Location: "Sportpark", Status: calendar.ICalConfirmed,
	}
}

var (
	future = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	past   = time.Date(2026, 10, 3, 6, 30, 0, 0, time.UTC)
)

func firstRun(t *testing.T, events ...calendar.Event) State {
	t.Helper()
	r := Diff(New(), events, t0, retain)
	if len(r.Changes) != len(events) {
		t.Fatalf("first run: %d changes, want %d", len(r.Changes), len(events))
	}
	for _, c := range r.Changes {
		if c.Kind != Added || c.Sequence != 0 {
			t.Fatalf("first run change: %v", c)
		}
	}
	return r.Next
}

func TestDiffUnchanged(t *testing.T) {
	prev := firstRun(t, ev("M1", future))
	r := Diff(prev, []calendar.Event{ev("M1", future)}, t1, retain)
	if r.Changed() {
		t.Fatalf("unexpected changes: %v", r.Changes)
	}
	e := r.Next.Events["M1@voetbal.nl"]
	if e.Sequence != 0 || !e.LastModified.Equal(t0) {
		t.Errorf("entry must keep seq/last-modified: %+v", e)
	}
	// Round-trip through JSON must also be a no-op.
	b, _ := prev.Marshal()
	loaded, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if Diff(loaded, []calendar.Event{ev("M1", future)}, t1, retain).Changed() {
		t.Error("state round-trip must not cause changes")
	}
}

func TestDiffRescheduled(t *testing.T) {
	prev := firstRun(t, ev("M1", future))
	moved := ev("M1", future.Add(2*time.Hour))
	r := Diff(prev, []calendar.Event{moved}, t1, retain)
	if len(r.Changes) != 1 || r.Changes[0].Kind != Updated || r.Changes[0].Sequence != 1 {
		t.Fatalf("changes: %v", r.Changes)
	}
	e := r.Next.Events["M1@voetbal.nl"]
	if e.Sequence != 1 || !e.LastModified.Equal(t1) || !e.Event.Start.Equal(moved.Start) {
		t.Errorf("entry: %+v", e)
	}
	if got := FieldsChanged(*r.Changes[0].Old, *r.Changes[0].New); strings.Join(got, ",") != "start,end" {
		t.Errorf("fields changed: %v", got)
	}
	// A second change bumps again.
	moved.Location = "Elders"
	r = Diff(r.Next, []calendar.Event{moved}, t1.Add(time.Hour), retain)
	if r.Next.Events["M1@voetbal.nl"].Sequence != 2 {
		t.Errorf("want seq 2, got %+v", r.Next.Events["M1@voetbal.nl"])
	}
}

func TestDiffCancelled(t *testing.T) {
	prev := firstRun(t, ev("M1", future))
	c := ev("M1", future)
	c.Status = calendar.ICalCancelled
	c.Summary += " (afgelast)"
	r := Diff(prev, []calendar.Event{c}, t1, retain)
	if len(r.Changes) != 1 || r.Changes[0].Kind != Updated {
		t.Fatalf("changes: %v", r.Changes)
	}
	e := r.Next.Events["M1@voetbal.nl"]
	if e.Event.Status != calendar.ICalCancelled || e.Sequence != 1 {
		t.Errorf("entry: %+v", e)
	}
	if len(r.Next.Items()) != 1 {
		t.Error("cancelled event must stay in the feed")
	}
}

func TestDiffNew(t *testing.T) {
	prev := firstRun(t, ev("M1", future))
	r := Diff(prev, []calendar.Event{ev("M1", future), ev("M2", future.Add(7*24*time.Hour))}, t1, retain)
	if len(r.Changes) != 1 || r.Changes[0].Kind != Added || r.Changes[0].UID != "M2@voetbal.nl" {
		t.Fatalf("changes: %v", r.Changes)
	}
	if e := r.Next.Events["M2@voetbal.nl"]; e.Sequence != 0 || !e.LastModified.Equal(t1) {
		t.Errorf("entry: %+v", e)
	}
}

func TestDiffRemovedPastIsRetained(t *testing.T) {
	prev := firstRun(t, ev("M1", past), ev("M2", future))
	r := Diff(prev, []calendar.Event{ev("M2", future)}, t1, retain)
	if r.Changed() {
		t.Fatalf("a played match dropping out of the programme is not a change: %v", r.Changes)
	}
	e, ok := r.Next.Events["M1@voetbal.nl"]
	if !ok || !e.Vanished || e.Sequence != 0 || e.Event.Status != calendar.ICalConfirmed {
		t.Errorf("past entry must be retained unchanged: %+v", e)
	}
	if len(r.Next.Items()) != 2 {
		t.Error("retained past event must stay in the feed")
	}
}

func TestDiffRemovedFutureIsCancelled(t *testing.T) {
	prev := firstRun(t, ev("M1", future), ev("M2", future.Add(time.Hour)))
	r := Diff(prev, []calendar.Event{ev("M2", future.Add(time.Hour))}, t1, retain)
	if len(r.Changes) != 1 || r.Changes[0].Kind != Vanished || r.Changes[0].Sequence != 1 {
		t.Fatalf("changes: %v", r.Changes)
	}
	e := r.Next.Events["M1@voetbal.nl"]
	if !e.Vanished || e.Event.Status != calendar.ICalCancelled || e.Event.Summary != "A – B (vervallen)" || e.Sequence != 1 {
		t.Errorf("entry: %+v", e)
	}
	// Still missing next run: no further change.
	r2 := Diff(r.Next, []calendar.Event{ev("M2", future.Add(time.Hour))}, t1.Add(12*time.Hour), retain)
	if r2.Changed() {
		t.Errorf("vanished entry must be stable: %v", r2.Changes)
	}
	// Reappears: un-cancelled with another bump.
	r3 := Diff(r2.Next, []calendar.Event{ev("M1", future), ev("M2", future.Add(time.Hour))}, t1.Add(24*time.Hour), retain)
	if len(r3.Changes) != 1 || r3.Changes[0].Kind != Updated {
		t.Fatalf("reappear: %v", r3.Changes)
	}
	if e := r3.Next.Events["M1@voetbal.nl"]; e.Vanished || e.Sequence != 2 || e.Event.Status != calendar.ICalConfirmed {
		t.Errorf("reappeared entry: %+v", e)
	}
}

func TestDiffPrunesAfterRetention(t *testing.T) {
	prev := firstRun(t, ev("M1", past))
	later := past.Add(retain + 2*time.Hour)
	r := Diff(prev, nil, later, retain)
	if len(r.Changes) != 1 || r.Changes[0].Kind != Pruned {
		t.Fatalf("changes: %v", r.Changes)
	}
	if len(r.Next.Events) != 0 {
		t.Errorf("pruned entry must be gone: %+v", r.Next.Events)
	}
	// Still in the API → never pruned.
	r = Diff(prev, []calendar.Event{ev("M1", past)}, later, retain)
	if r.Changed() || len(r.Next.Events) != 1 {
		t.Errorf("entry still returned by the API must not be pruned: %v", r.Changes)
	}
}

func TestDiffDuplicateUIDsKeepFirst(t *testing.T) {
	a, b := ev("M1", future), ev("M1", future.Add(time.Hour))
	r := Diff(New(), []calendar.Event{a, b}, t0, retain)
	if len(r.Changes) != 1 || !r.Next.Events["M1@voetbal.nl"].Event.Start.Equal(a.Start) {
		t.Errorf("duplicates: %v", r.Changes)
	}
}

func TestHasUpcoming(t *testing.T) {
	s := firstRun(t, ev("M1", past), ev("M2", future))
	if !s.HasUpcoming(t1) {
		t.Error("want upcoming")
	}
	if s.HasUpcoming(future.Add(time.Minute)) {
		t.Error("nothing upcoming after the last match")
	}
	e := s.Events["M2@voetbal.nl"]
	e.Vanished = true
	s.Events["M2@voetbal.nl"] = e
	if s.HasUpcoming(t1) {
		t.Error("vanished entries don't count")
	}
}

func TestParse(t *testing.T) {
	s, err := Parse(nil)
	if err != nil || s.Version != Version || s.Events == nil {
		t.Errorf("empty: %+v %v", s, err)
	}
	if _, err := Parse([]byte(`{"version": 99, "events": {}}`)); err == nil {
		t.Error("want version error")
	}
	if _, err := Parse([]byte(`{`)); err == nil {
		t.Error("want syntax error")
	}
}
