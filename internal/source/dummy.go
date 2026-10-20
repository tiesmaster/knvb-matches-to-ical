package source

import (
	"context"
	"fmt"
	"time"

	"github.com/tiesmaster/knvb-matches-to-ical/internal/fixtures"
	"github.com/tiesmaster/knvb-matches-to-ical/internal/sportlink"
)

// Dummy returns the real poolprogram.json match plus synthetic future matches.
// Dates are fixed (not relative to now) so repeated runs are stable.
type Dummy struct {
	// Reschedule moves the "rescheduled" match by an hour, to an other field,
	// to exercise SEQUENCE bumps in the local dry-run loop.
	Reschedule bool
}

// Synthetic match ids used by the dummy source.
const (
	DummyNormalID      = "M900000001"
	DummyRescheduledID = "M900000002"
	DummyCancelledID   = "M900000003"
)

func (d Dummy) Fixtures(context.Context) ([]sportlink.Fixture, error) {
	items, err := sportlink.DecodeProgram(fixtures.PoolProgram)
	if err != nil {
		return nil, fmt.Errorf("embedded poolprogram.json: %w", err)
	}
	var out []sportlink.Fixture
	for _, it := range items {
		out = append(out, sportlink.Fixture{Item: it})
	}

	ams := sportlink.Amsterdam()
	rescheduled := time.Date(2026, 11, 21, 9, 0, 0, 0, ams)
	field := "Veld 2"
	if d.Reschedule {
		rescheduled = rescheduled.Add(time.Hour)
		field = "Veld 4"
	}
	out = append(out,
		synthetic(DummyNormalID, "40001", time.Date(2026, 11, 14, 9, 0, 0, 0, ams), "SCHEDULED", true,
			&sportlink.MatchFacility{
				FacilityName: "Sportpark Neptunus-Schiebroek", SubFacilityName: "veld 1 A1",
				Address: "Hazelaarweg 40", ZipCode: "3053PM", City: "ROTTERDAM",
				AssemblyTime: "08:30", HomeDressingRoom: "3", AwayDressingRoom: "4", FieldType: "Kunstgras",
			}),
		synthetic(DummyRescheduledID, "40002", rescheduled, "SCHEDULED", false,
			&sportlink.MatchFacility{
				FacilityName: "Sportpark VOC", SubFacilityName: field,
				Address: "Hazelaarweg 7", ZipCode: "3053PM", City: "ROTTERDAM",
				AssemblyTime: "08:15", DepartureTime: "07:45",
			}),
		synthetic(DummyCancelledID, "40003", time.Date(2026, 11, 28, 10, 0, 0, 0, ams), "Afgelast", true, nil),
	)
	return out, nil
}

func synthetic(id, number string, start time.Time, status string, home bool, fac *sportlink.MatchFacility) sportlink.Fixture {
	us := sportlink.Team{
		PublicTeamID: "T1779014083", TeamCode: "O8-1", TeamName: "Neptunus-Schiebroek O8-1",
		Club: sportlink.Club{ClubID: "BBDX35H", ClubName: "Neptunus-Schiebroek", City: "ROTTERDAM"},
	}
	them := sportlink.Team{
		PublicTeamID: "T1049108481", TeamCode: "O8-5", TeamName: "VOC O8-5",
		Club: sportlink.Club{ClubID: "BBDZ847", ClubName: "VOC", City: "ROTTERDAM"},
	}
	if !home {
		us, them = them, us
	}
	ts := sportlink.Time{Time: start}
	return sportlink.Fixture{
		Item: sportlink.ProgramItemMatch{
			DetailsKey: id,
			Timestamp:  ts,
			Match: &sportlink.Match{
				PublicMatchID:   id,
				ExternalMatchID: sportlink.Text(number),
				MatchDateTime:   ts,
				Status:          status,
				Pool:            &sportlink.Pool{PoolID: 900001, PoolName: "7", ClassName: "4e klasse Zwaluwen Jeugd (2e fase) [synthetisch]"},
				HomeTeam:        us,
				AwayTeam:        them,
			},
		},
		Facility: fac,
	}
}
