// Package notify posts short ntfy messages about imminent match changes.
package notify

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/calendar"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/state"
)

// DefaultWindow is how far ahead a change must be to warrant a notification.
const DefaultWindow = 48 * time.Hour

// Ntfy posts to an ntfy topic URL.
type Ntfy struct {
	URL    string
	Title  string
	Window time.Duration
	HTTP   *http.Client
	Logger *slog.Logger
}

// New returns an ntfy notifier with a 48 h window.
func New(url, calName string, logger *slog.Logger) *Ntfy {
	return &Ntfy{URL: url, Title: calName, Window: DefaultWindow, HTTP: &http.Client{Timeout: 10 * time.Second}, Logger: logger}
}

// Notify posts one message listing the relevant changes, if any.
func (n *Ntfy) Notify(ctx context.Context, now time.Time, changes []state.Change) error {
	rel := Relevant(changes, now, n.Window)
	if len(rel) == 0 {
		return nil
	}
	lines := make([]string, 0, len(rel))
	for _, c := range rel {
		lines = append(lines, Message(c))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", mime.QEncoding.Encode("utf-8", n.Title+": wedstrijd gewijzigd"))
	req.Header.Set("Tags", "soccer")
	resp, err := n.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("ntfy: HTTP %d", resp.StatusCode)
	}
	if n.Logger != nil {
		n.Logger.Info("notification sent", "changes", len(rel))
	}
	return nil
}

var notifyFields = []string{"start", "location", "status"}

// Relevant keeps updated or vanished matches whose old or new kickoff lies in
// [now, now+window] and whose time, location or status changed.
func Relevant(changes []state.Change, now time.Time, window time.Duration) []state.Change {
	var out []state.Change
	for _, c := range changes {
		if c.Old == nil || c.New == nil {
			continue
		}
		if !inWindow(c.Old.Start, now, window) && !inWindow(c.New.Start, now, window) {
			continue
		}
		if slices.ContainsFunc(state.FieldsChanged(*c.Old, *c.New), func(f string) bool { return slices.Contains(notifyFields, f) }) {
			out = append(out, c)
		}
	}
	return out
}

func inWindow(t, now time.Time, window time.Duration) bool {
	return !t.Before(now) && !t.After(now.Add(window))
}

// Message renders one change, e.g.
// "za 14 nov 10:00 A – B: tijd 09:00 → 10:00; status → afgelast".
func Message(c state.Change) string {
	o, n := *c.Old, *c.New
	var parts []string
	if !o.Start.Equal(n.Start) {
		parts = append(parts, fmt.Sprintf("tijd %s → %s", when(o.Start), when(n.Start)))
	}
	if o.Location != n.Location {
		parts = append(parts, "locatie → "+cmpOr(n.Location, "onbekend"))
	}
	if o.Status != n.Status {
		parts = append(parts, "status → "+statusNL(n.Status))
	}
	return fmt.Sprintf("%s %s: %s", when(n.Start), n.Summary, strings.Join(parts, "; "))
}

var (
	weekdays = [...]string{"zo", "ma", "di", "wo", "do", "vr", "za"}
	months   = [...]string{"jan", "feb", "mrt", "apr", "mei", "jun", "jul", "aug", "sep", "okt", "nov", "dec"}
)

func when(t time.Time) string {
	t = t.In(sportlink.Amsterdam())
	return fmt.Sprintf("%s %d %s %s", weekdays[t.Weekday()], t.Day(), months[t.Month()-1], t.Format("15:04"))
}

func statusNL(s string) string {
	switch s {
	case calendar.ICalCancelled:
		return "afgelast"
	case calendar.ICalTentative:
		return "onzeker"
	default:
		return "doorgaand"
	}
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
