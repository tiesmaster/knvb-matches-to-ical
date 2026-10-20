package calendar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink"
)

// UIDSuffix is appended to PublicMatchId to form the VEVENT UID.
const UIDSuffix = "@voetbal.nl"

// Event holds exactly the mapped VEVENT fields. Its JSON form is what gets
// hashed for change detection, so DTSTAMP, SEQUENCE and LAST-MODIFIED are
// deliberately not part of it.
type Event struct {
	UID         string    `json:"uid"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Summary     string    `json:"summary"`
	Location    string    `json:"location,omitempty"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
}

// Hash is a stable content hash of the event.
func (e Event) Hash() string {
	e.Start, e.End = e.Start.UTC(), e.End.UTC()
	b, err := json.Marshal(e)
	if err != nil {
		panic(err) // plain struct of strings and times; cannot fail
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// MapOptions configures ToEvent.
type MapOptions struct {
	DefaultDuration time.Duration
	Logger          *slog.Logger
}

// ToEvent maps a fixture to an Event (REQUIREMENTS §4.2).
func ToEvent(f sportlink.Fixture, opts MapOptions) Event {
	m := f.Item.Match
	kind := ClassifyStatus(m.Status)
	if kind == StatusUnknown && opts.Logger != nil {
		opts.Logger.Warn("unknown match status; treating as scheduled", "match", m.PublicMatchID, "status", m.Status)
	}

	start := f.Start().UTC()
	end := f.Item.EndTimestamp.UTC()
	if !end.After(start) {
		end = start.Add(opts.DefaultDuration)
	}

	return Event{
		UID:         m.PublicMatchID + UIDSuffix,
		Start:       start,
		End:         end,
		Summary:     fmt.Sprintf("%s – %s%s", teamName(m.HomeTeam), teamName(m.AwayTeam), kind.summarySuffix()),
		Location:    location(m, f.Facility),
		Description: description(m, f.Facility, kind),
		Status:      kind.ICalStatus(),
	}
}

func teamName(t sportlink.Team) string {
	if t.TeamName != "" {
		return t.TeamName
	}
	if t.Club.ClubName != "" {
		return strings.TrimSpace(t.Club.ClubName + " " + t.TeamCode)
	}
	return "?"
}

// location is "{FacilityName} ({SubFacilityName}), {Address}, {ZipCode} {City}",
// skipping empty parts, falling back to the programme's rough Match.Location.
func location(m *sportlink.Match, f *sportlink.MatchFacility) string {
	if f == nil {
		return m.Location
	}
	name := clean(f.FacilityName)
	if sub := clean(f.SubFacilityName); sub != "" {
		name = joinNonEmpty(" ", name, "("+sub+")")
	}
	s := joinNonEmpty(", ", name, clean(f.Address), joinNonEmpty(" ", clean(f.ZipCode), clean(f.City)))
	if s == "" {
		return m.Location
	}
	return s
}

func description(m *sportlink.Match, f *sportlink.MatchFacility, kind StatusKind) string {
	var lines []string
	add := func(label, value string) {
		if value = strings.TrimSpace(value); value != "" {
			lines = append(lines, label+": "+value)
		}
	}
	if kind != StatusNormal {
		add("Status", m.Status)
	}
	if f != nil {
		add("Verzamelen", clock(f.AssemblyTime))
		add("Vertrek", clock(f.DepartureTime))
		add("Kleedkamer thuis", clean(f.HomeDressingRoom))
		add("Kleedkamer uit", clean(f.AwayDressingRoom))
		add("Kleedkamer scheidsrechter", clean(f.OfficialDressingRoom))
		add("Veldtype", clean(f.FieldType))
	}
	if p := m.Pool; p != nil {
		pool := p.ClassName
		if p.PoolName != "" {
			pool = joinNonEmpty(", ", pool, "poule "+p.PoolName)
		}
		add("Competitie", pool)
	}
	add("Wedstrijdnummer", string(m.ExternalMatchID))
	add("Tuchtstatus", m.DisciplineStatus)
	if f != nil {
		add("Clubinfo", clean(f.ClubInformationURL))
	}
	return strings.Join(lines, "\n")
}

// clock renders a time-ish value as "15:04" in Europe/Amsterdam when it parses
// as a Sportlink timestamp; otherwise (e.g. it is already "08:00") verbatim.
func clock(t string) string {
	s := clean(t)
	if s == "" {
		return ""
	}
	if ts, err := sportlink.ParseTime(s); err == nil {
		return ts.In(sportlink.Amsterdam()).Format("15:04")
	}
	return s
}

func clean(s string) string { return strings.TrimSpace(s) }

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
