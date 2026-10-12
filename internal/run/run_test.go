package run

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/source"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/state"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/store"
)

type memStore struct {
	objects map[string][]byte
	puts    []string
	failPut string
}

func (m *memStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	b, ok := m.objects[key]
	return b, ok, nil
}

func (m *memStore) Put(_ context.Context, key string, body []byte, contentType, cacheControl string) error {
	if key == m.failPut {
		return errors.New("boom")
	}
	m.puts = append(m.puts, key+" "+contentType+" "+cacheControl)
	m.objects[key] = body
	return nil
}

type fixedSource struct {
	fixtures []sportlink.Fixture
	err      error
}

func (f fixedSource) Fixtures(context.Context) ([]sportlink.Fixture, error) { return f.fixtures, f.err }

type recNotifier struct{ calls [][]state.Change }

func (r *recNotifier) Notify(_ context.Context, _ time.Time, c []state.Change) error {
	r.calls = append(r.calls, c)
	return nil
}

var now = time.Date(2026, 10, 12, 4, 17, 0, 0, time.UTC)

func runner(src source.FixtureSource, st store.Store) *Runner {
	return &Runner{
		Source: src,
		Store:  st,
		Now:    func() time.Time { return now },
		Logger: slog.New(slog.DiscardHandler),
		Options: Options{
			FeedKey: "x7f3k/o8-1.ics", StateKey: "x7f3k/state.json", CalName: "Test",
			TeamID: "T1779014083", DefaultDuration: time.Hour, Retain: 180 * 24 * time.Hour,
		},
	}
}

func dummy(t *testing.T, reschedule bool) []sportlink.Fixture {
	f, err := source.Dummy{Reschedule: reschedule}.Fixtures(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRunPublishesThenIdles(t *testing.T) {
	st := &memStore{objects: map[string][]byte{}}
	n := &recNotifier{}
	r := runner(fixedSource{fixtures: dummy(t, false)}, st)
	r.Notifier = n

	res, err := r.Run(context.Background())
	if err != nil || !res.Published || res.Events != 4 || len(res.Changes) != 4 {
		t.Fatalf("first run: %+v %v", res, err)
	}
	want := []string{
		"x7f3k/o8-1.ics text/calendar; charset=utf-8 max-age=300",
		"x7f3k/state.json application/json no-store",
	}
	if strings.Join(st.puts, "|") != strings.Join(want, "|") {
		t.Errorf("puts (feed must come first): %v", st.puts)
	}
	ics := string(st.objects["x7f3k/o8-1.ics"])
	if strings.Count(ics, "BEGIN:VEVENT") != 4 || !strings.Contains(ics, "STATUS:CANCELLED") {
		t.Errorf("feed:\n%s", ics)
	}

	st.puts = nil
	r.Now = func() time.Time { return now.Add(12 * time.Hour) }
	res, err = r.Run(context.Background())
	if err != nil || res.Published || len(st.puts) != 0 {
		t.Fatalf("unchanged run must upload nothing: %+v %v %v", res, err, st.puts)
	}

	r.Source = fixedSource{fixtures: dummy(t, true)}
	res, err = r.Run(context.Background())
	if err != nil || !res.Published || len(res.Changes) != 1 || res.Changes[0].Kind != state.Updated {
		t.Fatalf("reschedule: %+v %v", res, err)
	}
	if !strings.Contains(string(st.objects["x7f3k/o8-1.ics"]), "SEQUENCE:1") {
		t.Error("rescheduled event must have SEQUENCE:1")
	}
	if len(n.calls) != 2 {
		t.Errorf("notifier calls = %d, want 2 (only published runs)", len(n.calls))
	}
}

func TestRunFetchFailureWritesNothing(t *testing.T) {
	st := &memStore{objects: map[string][]byte{"x7f3k/o8-1.ics": []byte("old")}}
	_, err := runner(fixedSource{err: errors.New("api down")}, st).Run(context.Background())
	if err == nil || len(st.puts) != 0 || string(st.objects["x7f3k/o8-1.ics"]) != "old" {
		t.Fatalf("err=%v puts=%v", err, st.puts)
	}
}

func TestRunEmptyGuard(t *testing.T) {
	st := &memStore{objects: map[string][]byte{}}
	r := runner(fixedSource{fixtures: dummy(t, false)}, st)
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	st.puts = nil
	r.Source = fixedSource{}
	if _, err := r.Run(context.Background()); !errors.Is(err, ErrEmptyFeed) || len(st.puts) != 0 {
		t.Fatalf("want ErrEmptyFeed and no uploads, got %v %v", err, st.puts)
	}
	r.Options.AllowEmpty = true
	res, err := r.Run(context.Background())
	if err != nil || !res.Published {
		t.Fatalf("ALLOW_EMPTY: %+v %v", res, err)
	}
	// Upcoming matches that vanished are now cancelled; the played one is retained.
	ics := string(st.objects["x7f3k/o8-1.ics"])
	if strings.Count(ics, "STATUS:CANCELLED") != 3 || strings.Count(ics, "BEGIN:VEVENT") != 4 {
		t.Errorf("feed:\n%s", ics)
	}
}

func TestRunFirstRunEmptyPublishes(t *testing.T) {
	st := &memStore{objects: map[string][]byte{}}
	res, err := runner(fixedSource{}, st).Run(context.Background())
	if err != nil || !res.Published || !bytes.Contains(st.objects["x7f3k/o8-1.ics"], []byte("BEGIN:VCALENDAR")) {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRunOnlyOwnTeam(t *testing.T) {
	st := &memStore{objects: map[string][]byte{}}
	r := runner(fixedSource{fixtures: dummy(t, false)}, st)
	r.Options.OnlyOwnTeam = true
	res, err := r.Run(context.Background())
	if err != nil || res.Events != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	if bytes.Contains(st.objects["x7f3k/o8-1.ics"], []byte("M707488589")) {
		t.Error("pool match without our team must be filtered")
	}
}

func TestRunStateUploadFailure(t *testing.T) {
	st := &memStore{objects: map[string][]byte{}, failPut: "x7f3k/state.json"}
	if _, err := runner(fixedSource{fixtures: dummy(t, false)}, st).Run(context.Background()); err == nil {
		t.Fatal("state upload failure must fail the run")
	}
}

func TestRunCorruptStateFails(t *testing.T) {
	st := &memStore{objects: map[string][]byte{"x7f3k/state.json": []byte("{not json")}}
	if _, err := runner(fixedSource{fixtures: dummy(t, false)}, st).Run(context.Background()); err == nil || len(st.puts) != 0 {
		t.Fatalf("err=%v puts=%v", err, st.puts)
	}
}
