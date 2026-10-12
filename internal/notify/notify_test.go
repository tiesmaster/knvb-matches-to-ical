package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/calendar"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/state"
)

var now = time.Date(2026, 11, 13, 6, 17, 0, 0, time.UTC) // Friday morning

func change(kind state.ChangeKind, old, new calendar.Event) state.Change {
	return state.Change{UID: old.UID, Kind: kind, Old: &old, New: &new}
}

func base(start time.Time) calendar.Event {
	return calendar.Event{UID: "M1@voetbal.nl", Start: start, End: start.Add(time.Hour), Summary: "A – B", Location: "Sportpark", Status: calendar.ICalConfirmed}
}

func TestRelevant(t *testing.T) {
	sat := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC) // 09:00 Amsterdam, within 48 h
	far := sat.Add(7 * 24 * time.Hour)

	moved := base(sat)
	moved.Start = sat.Add(time.Hour)
	cancelled := base(sat)
	cancelled.Status = calendar.ICalCancelled
	descOnly := base(sat)
	descOnly.Description = "Verzamelen: 08:30"
	farMoved := base(far)
	farMoved.Start = far.Add(time.Hour)
	intoWindow := base(far)
	intoWindow.Start = sat

	changes := []state.Change{
		change(state.Updated, base(sat), moved),                // relevant
		change(state.Vanished, base(sat), cancelled),           // relevant
		change(state.Updated, base(sat), descOnly),             // description only: no
		change(state.Updated, base(far), farMoved),             // outside window: no
		change(state.Updated, base(far), intoWindow),           // moved into window: yes
		{UID: "M9@voetbal.nl", Kind: state.Added, New: &moved}, // added: no
	}
	got := Relevant(changes, now, DefaultWindow)
	if len(got) != 3 {
		t.Fatalf("got %d relevant, want 3: %v", len(got), got)
	}
}

func TestMessage(t *testing.T) {
	sat := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)
	n := base(sat.Add(time.Hour))
	n.Location = "Elders"
	n.Status = calendar.ICalCancelled
	got := Message(change(state.Updated, base(sat), n))
	want := "za 14 nov 10:00 A – B: tijd za 14 nov 09:00 → za 14 nov 10:00; locatie → Elders; status → afgelast"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestNotifyPosts(t *testing.T) {
	var body, title string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		body, title = string(b), r.Header.Get("Title")
	}))
	defer srv.Close()
	n := New(srv.URL, "Neptunus O8-1", nil)

	sat := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)
	c := base(sat)
	c.Status = calendar.ICalCancelled
	if err := n.Notify(context.Background(), now, []state.Change{change(state.Updated, base(sat), c)}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(body, "status → afgelast") || title != "Neptunus O8-1: wedstrijd gewijzigd" {
		t.Errorf("calls=%d title=%q body=%q", calls, title, body)
	}
	// Nothing relevant → no POST.
	if err := n.Notify(context.Background(), now, nil); err != nil || calls != 1 {
		t.Errorf("unexpected POST: %v calls=%d", err, calls)
	}
}

func TestNotifyHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()
	sat := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)
	c := base(sat)
	c.Status = calendar.ICalCancelled
	if err := New(srv.URL, "x", nil).Notify(context.Background(), now, []state.Change{change(state.Updated, base(sat), c)}); err == nil {
		t.Error("want error on 403")
	}
}
