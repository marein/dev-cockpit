package assistant

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A cron subscription carries a schedule in the five field form every crontab
// reads: minute, hour, day of month, month, day of week, each a star, a
// number, a range, a step or a list of those. Read here and not by a library
// because a dependency for five fields is more code than the fields: the
// cockpit only ever asks one question of a schedule, when the next tick after
// a moment is, and the answer is computed by walking the minutes.

// cronSchedule is one parsed schedule: which values every field admits.
type cronSchedule struct {
	minute, hour, dom, month, dow [64]bool
	// domAny and dowAny say the field was a star. Cron reads a day as
	// matching when either restricted day field matches, and when only one
	// of the two is restricted that one decides alone.
	domAny, dowAny bool
}

// cronBound is the range of every field, low to high.
var cronBound = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}

// ParseCron reads a schedule and refuses what no crontab would run. The
// sentence names the field, because that is what somebody typing one needs.
func ParseCron(spec string) (*cronSchedule, error) {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return nil, fmt.Errorf("A schedule has five fields, minute hour day month weekday, like \"*/30 9-17 * * 1-5\"; %q has %d.", spec, len(fields))
	}
	s := &cronSchedule{}
	names := []string{"minute", "hour", "day of month", "month", "day of week"}
	sets := []*[64]bool{&s.minute, &s.hour, &s.dom, &s.month, &s.dow}
	for i, field := range fields {
		any, err := parseCronField(field, cronBound[i][0], cronBound[i][1], sets[i])
		if err != nil {
			return nil, fmt.Errorf("The %s field %q of the schedule cannot be read: %s", names[i], field, err.Error())
		}
		switch i {
		case 2:
			s.domAny = any
		case 4:
			s.dowAny = any
			// Both 0 and 7 are Sunday.
			if s.dow[7] {
				s.dow[0] = true
			}
		}
	}
	return s, nil
}

// parseCronField fills set with the values one field admits and reports
// whether the field was a bare star.
func parseCronField(field string, low, high int, set *[64]bool) (bool, error) {
	if field == "*" {
		for v := low; v <= high; v++ {
			set[v] = true
		}
		return true, nil
	}
	for _, part := range strings.Split(field, ",") {
		step := 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return false, errors.New("the step after / has to be a number above zero")
			}
			step = n
			part = part[:i]
		}
		from, to := low, high
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			a, b, ok := strings.Cut(part, "-")
			var err error
			if from, err = strconv.Atoi(a); err != nil {
				return false, errors.New("a range starts with a number")
			}
			if to, err = strconv.Atoi(b); err != nil {
				return false, errors.New("a range ends with a number")
			}
			if ok && from > to {
				return false, errors.New("a range runs from low to high")
			}
		default:
			n, err := strconv.Atoi(part)
			if err != nil {
				return false, errors.New("a value is a number, a range, a star or a list of those")
			}
			from, to = n, n
			if step > 1 {
				to = high
			}
		}
		if from < low || to > high {
			return false, fmt.Errorf("a value has to be between %d and %d", low, high)
		}
		for v := from; v <= to; v += step {
			set[v] = true
		}
	}
	return false, nil
}

// cronHorizon bounds the walk for the next tick: a schedule that matches no
// minute within it, February 30th, is refused as one that never runs.
const cronHorizon = 366 * 24 * time.Hour

// Next is the first tick after the moment, in the local zone, which is the
// zone a person writes a schedule in. It walks the minutes and skips whole
// days and hours the schedule cannot match, so a sparse schedule is not a
// half million comparisons.
func (s *cronSchedule) Next(after time.Time) (time.Time, bool) {
	t := after.In(time.Local).Truncate(time.Minute).Add(time.Minute)
	end := t.Add(cronHorizon)
	for t.Before(end) {
		if !s.month[int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.hour[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !s.minute[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t, true
	}
	return time.Time{}, false
}

func (s *cronSchedule) dayMatches(t time.Time) bool {
	dom := s.dom[t.Day()]
	dow := s.dow[int(t.Weekday())]
	switch {
	case s.domAny && s.dowAny:
		return true
	case s.domAny:
		return dow
	case s.dowAny:
		return dom
	}
	return dom || dow
}

// NextCron is the next tick of a schedule after the moment, for a caller
// that holds the text and not the parse.
func NextCron(spec string, after time.Time) (time.Time, error) {
	schedule, err := ParseCron(spec)
	if err != nil {
		return time.Time{}, err
	}
	next, ok := schedule.Next(after)
	if !ok {
		return time.Time{}, fmt.Errorf("The schedule %q never runs: no minute in the coming year matches it.", spec)
	}
	return next, nil
}
