package uccalendar

import (
	"time"

	"github.com/HectorVeraO/chatisimo/internal/uccalendar/internal/espn"
)

type resolvedQuery struct {
	mode        QueryMode
	window      WindowKind
	queriedDate LocalDate
	rangeStart  time.Time
	rangeEnd    time.Time
}

func resolveQueryRange(q Query, now time.Time, loc *time.Location) (resolvedQuery, error) {
	if q.Window != WindowDefault && q.Date != "" {
		return resolvedQuery{}, &InputError{Field: "query", Code: InputCodeMutuallyExclusive}
	}

	today := localDateIn(now, loc)

	if q.Date != "" {
		parsedDate, err := ParseDate(q.Date, today.Year)
		if err != nil {
			return resolvedQuery{}, err
		}

		res := resolvedQuery{
			mode:        ModeDate,
			window:      WindowDefault,
			queriedDate: parsedDate,
			rangeStart:  parsedDate.StartOfDay(loc),
			rangeEnd:    parsedDate.EndOfDay(loc),
		}

		return res, nil
	}

	window := q.Window
	if window == WindowDefault {
		window = WindowThisWeekend
	}

	switch window {
	case WindowToday:
		res := resolvedQuery{
			mode:       ModeWindow,
			window:     WindowToday,
			rangeStart: today.StartOfDay(loc),
			rangeEnd:   today.EndOfDay(loc),
		}

		return res, nil
	case WindowTomorrow:
		tomorrow := today.AddDays(1)
		res := resolvedQuery{
			mode:       ModeWindow,
			window:     WindowTomorrow,
			rangeStart: tomorrow.StartOfDay(loc),
			rangeEnd:   tomorrow.EndOfDay(loc),
		}

		return res, nil
	case WindowThisWeekend:
		friday, sunday := weekendDatesForWeek(today, loc, 0)
		res := resolvedQuery{
			mode:       ModeWindow,
			window:     WindowThisWeekend,
			rangeStart: fridayEveningStart(friday, loc),
			rangeEnd:   sunday.EndOfDay(loc),
		}

		return res, nil
	case WindowNextWeekend:
		friday, sunday := weekendDatesForWeek(today, loc, 1)
		res := resolvedQuery{
			mode:       ModeWindow,
			window:     WindowNextWeekend,
			rangeStart: fridayEveningStart(friday, loc),
			rangeEnd:   sunday.EndOfDay(loc),
		}

		return res, nil
	case WindowThisMonth:
		firstDay := LocalDate{Year: today.Year, Month: today.Month, Day: 1}
		lastDayTime := time.Date(today.Year, today.Month+1, 0, 12, 0, 0, 0, time.UTC)
		lastDay := LocalDate{
			Year:  lastDayTime.Year(),
			Month: lastDayTime.Month(),
			Day:   lastDayTime.Day(),
		}

		res := resolvedQuery{
			mode:       ModeWindow,
			window:     WindowThisMonth,
			rangeStart: firstDay.StartOfDay(loc),
			rangeEnd:   lastDay.EndOfDay(loc),
		}

		return res, nil
	default:
		return resolvedQuery{}, &InputError{Field: "window", Code: InputCodeUnknown}
	}
}

func weekendDatesForWeek(today LocalDate, loc *time.Location, weekOffset int) (LocalDate, LocalDate) {
	weekday := today.Weekday(loc)
	mondayOffset := (int(weekday) + 6) % 7
	friday := today.AddDays((4 - mondayOffset) + (weekOffset * 7))
	sunday := friday.AddDays(2)

	return friday, sunday
}

func fridayEveningStart(friday LocalDate, loc *time.Location) time.Time {
	start := time.Date(friday.Year, friday.Month, friday.Day, 18, 0, 0, 0, loc)
	return start
}

func localDateIn(t time.Time, loc *time.Location) LocalDate {
	inLoc := t.In(loc)
	d := LocalDate{
		Year:  inLoc.Year(),
		Month: inLoc.Month(),
		Day:   inLoc.Day(),
	}

	return d
}

func eventToMatch(ev espn.Event) Match {
	cond := VenueAway
	pre := awayPreBlock
	post := awayPostBlock
	if ev.Home {
		cond = VenueHome
		pre = homePreBlock
		post = homePostBlock
	}

	date := LocalDate{
		Year:  ev.Year,
		Month: ev.Month,
		Day:   ev.Day,
	}

	if ev.TBD {
		tbdMatch := Match{
			ID:         ev.ID,
			Opponent:   ev.Opponent,
			Tournament: ev.Tournament,
			Venue:      ev.Venue,
			Condition:  cond,
			TBD:        true,
			Date:       date,
		}

		return tbdMatch
	}

	confirmed := Match{
		ID:         ev.ID,
		Opponent:   ev.Opponent,
		Tournament: ev.Tournament,
		Venue:      ev.Venue,
		Condition:  cond,
		TBD:        false,
		Date:       date,
		Kickoff:    ev.Kickoff,
		BlockStart: ev.Kickoff.Add(-pre),
		BlockEnd:   ev.Kickoff.Add(post),
	}

	return confirmed
}

func overlappingMatches(schedule []Match, rangeStart, rangeEnd time.Time, loc *time.Location) []Match {
	startDate := localDateIn(rangeStart, loc)
	endDate := localDateIn(rangeEnd, loc)

	var matches []Match

	for _, m := range schedule {
		if matchOverlapsRange(m, rangeStart, rangeEnd, startDate, endDate) {
			matches = append(matches, m)
		}
	}

	return matches
}

func matchOverlapsRange(
	m Match,
	rangeStart time.Time,
	rangeEnd time.Time,
	startDate LocalDate,
	endDate LocalDate,
) bool {
	if m.TBD {
		inDateSpan := m.Date.Compare(startDate) >= 0 && m.Date.Compare(endDate) <= 0
		return inDateSpan
	}

	overlaps := !m.BlockStart.After(rangeEnd) && !m.BlockEnd.Before(rangeStart)
	return overlaps
}

func statusForMatches(matches []Match) AvailabilityStatus {
	hasTBD := false
	for _, m := range matches {
		if !m.TBD {
			return StatusBlocked
		}

		hasTBD = true
	}

	if hasTBD {
		return StatusPendingANFP
	}

	return StatusFree
}

func findNextMatch(
	schedule []Match,
	now time.Time,
	today LocalDate,
	rangeStart time.Time,
	rangeEnd time.Time,
	includeBeforeRange bool,
	loc *time.Location,
) *Match {
	startDate := localDateIn(rangeStart, loc)
	endDate := localDateIn(rangeEnd, loc)

	for _, m := range schedule {
		if matchOverlapsRange(m, rangeStart, rangeEnd, startDate, endDate) {
			continue
		}

		if m.TBD {
			isFuture := m.Date.Compare(today) >= 0 && (includeBeforeRange || m.Date.Compare(endDate) > 0)
			if isFuture {
				next := m
				return &next
			}

			continue
		}

		isUpcoming := m.Kickoff.After(now) && (includeBeforeRange || m.Kickoff.After(rangeEnd))
		if isUpcoming {
			next := m
			return &next
		}
	}

	return nil
}

func cloneMatches(src []Match) []Match {
	if len(src) == 0 {
		return nil
	}

	dst := make([]Match, len(src))
	copy(dst, src)

	return dst
}
