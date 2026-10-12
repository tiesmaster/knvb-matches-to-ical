// Package calendar maps Sportlink fixtures to calendar events and renders them
// as an iCalendar feed.
package calendar

import "strings"

// StatusKind classifies the free-form Match.Status string.
type StatusKind int

const (
	StatusNormal StatusKind = iota
	StatusCancelled
	StatusAbandoned
	StatusPostponed
	StatusUnknown
)

// iCalendar STATUS values.
const (
	ICalConfirmed = "CONFIRMED"
	ICalTentative = "TENTATIVE"
	ICalCancelled = "CANCELLED"
)

// Status strings are free-form on the server (REQUIREMENTS §4.3). The only value
// seen live so far is "SCHEDULED", so both the English and Dutch vocabularies
// are accepted.
var statusWords = map[string]StatusKind{
	"":           StatusNormal,
	"scheduled":  StatusNormal,
	"gepland":    StatusNormal,
	"played":     StatusNormal,
	"gespeeld":   StatusNormal,
	"afgelast":   StatusCancelled,
	"cancelled":  StatusCancelled,
	"canceled":   StatusCancelled,
	"gestaakt":   StatusAbandoned,
	"abandoned":  StatusAbandoned,
	"uitgesteld": StatusPostponed,
	"verplaatst": StatusPostponed,
	"postponed":  StatusPostponed,
}

// ClassifyStatus maps Match.Status case-insensitively. Unknown values return
// StatusUnknown; callers should log them and treat the match as normal.
func ClassifyStatus(raw string) StatusKind {
	if k, ok := statusWords[strings.ToLower(strings.TrimSpace(raw))]; ok {
		return k
	}
	return StatusUnknown
}

// ICalStatus returns the VEVENT STATUS for a status kind.
func (k StatusKind) ICalStatus() string {
	switch k {
	case StatusCancelled, StatusAbandoned:
		return ICalCancelled
	case StatusPostponed:
		return ICalTentative
	default:
		return ICalConfirmed
	}
}

// summarySuffix marks non-normal matches in SUMMARY, for clients that hide or
// ignore STATUS.
func (k StatusKind) summarySuffix() string {
	switch k {
	case StatusCancelled:
		return " (afgelast)"
	case StatusAbandoned:
		return " (gestaakt)"
	case StatusPostponed:
		return " (uitgesteld)"
	default:
		return ""
	}
}
