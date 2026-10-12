package calendar

import (
	"cmp"
	"slices"
	"time"

	ics "github.com/arran4/golang-ical"
)

// ProdID identifies this tool in the feed.
const ProdID = "-//tiesmaster//knvb-matches-to-ical//NL"

// Item is an event with its versioning metadata from the state.
type Item struct {
	Event        Event
	Sequence     int
	LastModified time.Time
}

// Feed describes the calendar as a whole.
type Feed struct {
	Name string
}

// Render produces the .ics body. Output is deterministic for a given input:
// items are sorted by start time then UID, and DTSTAMP is the item's
// LAST-MODIFIED (the moment this revision was created), so an unchanged state
// renders byte-for-byte identical feeds.
func Render(feed Feed, items []Item) []byte {
	items = slices.Clone(items)
	slices.SortFunc(items, func(a, b Item) int {
		return cmp.Or(a.Event.Start.Compare(b.Event.Start), cmp.Compare(a.Event.UID, b.Event.UID))
	})

	cal := ics.NewCalendarFor("tiesmaster")
	cal.SetProductId(ProdID)
	cal.SetMethod(ics.MethodPublish)
	cal.SetCalscale("GREGORIAN")
	if feed.Name != "" {
		cal.SetName(feed.Name) // NAME and X-WR-CALNAME
	}
	cal.SetXWRTimezone("Europe/Amsterdam")
	cal.SetRefreshInterval("PT6H")
	cal.SetXPublishedTTL("PT6H")

	for _, it := range items {
		e := it.Event
		ve := cal.AddEvent(e.UID)
		ve.SetDtStampTime(it.LastModified)
		ve.SetLastModifiedAt(it.LastModified)
		ve.SetSequence(it.Sequence)
		ve.SetStartAt(e.Start)
		ve.SetEndAt(e.End)
		ve.SetSummary(e.Summary)
		if e.Location != "" {
			ve.SetLocation(e.Location)
		}
		if e.Description != "" {
			ve.SetDescription(e.Description)
		}
		ve.SetStatus(ics.ObjectStatus(e.Status))
		ve.SetTimeTransparency(ics.TransparencyOpaque)
	}
	return []byte(cal.Serialize(ics.WithNewLineWindows))
}
