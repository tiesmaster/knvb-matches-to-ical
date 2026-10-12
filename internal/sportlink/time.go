package sportlink

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// TimeLayout is the format Sportlink uses, e.g. "2026-10-03T08:30:00+0200".
// The offset has no colon, so it is not RFC 3339.
const TimeLayout = "2006-01-02T15:04:05-0700"

var fallbackLayouts = []string{
	time.RFC3339,          // in case the server ever adds the colon
	"2006-01-02T15:04:05", // no offset: interpreted in Europe/Amsterdam
	"2006-01-02T15:04",
}

var amsterdam = sync.OnceValue(func() *time.Location {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		panic(fmt.Sprintf("load Europe/Amsterdam (import time/tzdata): %v", err))
	}
	return loc
})

// Amsterdam returns the Europe/Amsterdam location.
func Amsterdam() *time.Location { return amsterdam() }

// ParseTime parses a Sportlink timestamp.
func ParseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(TimeLayout, s); err == nil {
		return t, nil
	}
	for _, l := range fallbackLayouts {
		if t, err := time.ParseInLocation(l, s, Amsterdam()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp %q (want layout %s)", s, TimeLayout)
}

// Time is a nullable Sportlink timestamp; null or "" decode to the zero value.
type Time struct{ time.Time }

func (t *Time) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		*t = Time{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("timestamp: %w", err)
	}
	if s == "" {
		*t = Time{}
		return nil
	}
	p, err := ParseTime(s)
	if err != nil {
		return err
	}
	t.Time = p
	return nil
}

func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.Format(TimeLayout))
}
