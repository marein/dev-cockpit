package statusline

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

// reading is one value as it stands on the line: the text, and for a number
// the value times a hundred, rounded where the text rounds, which is what a
// bound is compared against. A cost of 1.499 stands as $1.50 and so reaches a
// bound at 1.50, the same as a rise worked out in whole cents does.
type reading struct {
	text   string
	scaled int64
}

func textReading(text string) (reading, bool) {
	text = oneLine(text)
	return reading{text: text}, text != ""
}

func percent(x float64) (reading, bool) {
	hundredths := math.Round(x * 100)
	return reading{text: decimal(hundredths/100) + "%", scaled: int64(hundredths)}, true
}

// money is always to the cent, so a dollar and a half is $1.50 and never $1.5
// beside the last request's $0.13.
func money(x float64) (reading, bool) {
	cents := int64(math.Round(x * 100))
	return reading{text: "$" + dollars(cents), scaled: cents}, true
}

func rate(x float64) (reading, bool) {
	r, ok := money(x)
	r.text += "/h"
	return r, ok
}

// count writes a number of things the way the line has room for: below a
// thousand as it is, above in the first unit it stays below a thousand of, so
// 999950 tokens are 1M and never 1000k.
func count(x float64) (reading, bool) {
	r := reading{scaled: int64(math.Round(x * 100))}
	if x < 1000 {
		r.text = strconv.FormatInt(int64(math.Floor(x)), 10)
		return r, true
	}
	units := []struct {
		name string
		size float64
	}{{"k", 1e3}, {"M", 1e6}, {"G", 1e9}}
	for i, unit := range units {
		v := math.Round(x/unit.size*10) / 10
		if v < 1000 || i == len(units)-1 {
			r.text = decimal(v) + unit.name
			break
		}
	}
	return r, true
}

// span is a length of time in its one largest unit, and its bounds are
// minutes.
func span(secs int64) (reading, bool) {
	if secs < 0 {
		return reading{}, false
	}
	var text string
	switch {
	case secs >= 86400:
		text = strconv.FormatInt(secs/86400, 10) + "d"
	case secs >= 3600:
		text = strconv.FormatInt(secs/3600, 10) + "h"
	case secs >= 60:
		text = strconv.FormatInt(secs/60, 10) + "m"
	default:
		text = strconv.FormatInt(secs, 10) + "s"
	}
	return reading{text: text, scaled: secs * 100 / 60}, true
}

// until and since are the seconds to and from a moment, never below zero.
func until(stamp, now int64) (reading, bool) {
	if stamp <= 0 {
		return reading{}, false
	}
	return span(max(stamp-now, 0))
}

func since(stamp, now int64) (reading, bool) {
	if stamp <= 0 {
		return reading{}, false
	}
	return span(max(now-stamp, 0))
}

// decimal writes a number in its shortest form, 16 and not 16.00, 42.5 and not
// 42.50.
func decimal(x float64) string {
	return strconv.FormatFloat(x, 'f', -1, 64)
}

func dollars(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func itoa(n int) string { return strconv.Itoa(n) }

// field walks a decoded JSON document down a path of object keys and answers
// nil wherever the document says nothing there or is shaped otherwise, which
// is what makes an entry fall out of the line instead of taking it along.
func field(doc any, path ...string) any {
	for _, key := range path {
		object, ok := doc.(map[string]any)
		if !ok {
			return nil
		}
		doc = object[key]
	}
	return doc
}

func number(v any) (float64, bool) {
	x, ok := v.(float64)
	return x, ok
}

// str answers a string, and a number written out, the way claude may send a
// version one day.
func str(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case float64:
		return decimal(t), true
	}
	return "", false
}

func orZero(v any) float64 {
	x, _ := number(v)
	return x
}

// offset is the zone an ISO time ends in, which the fallback below needs
// because it reads the clock time on its own.
var offset = regexp.MustCompile(`([+-])([0-9]{2}):?([0-9]{2})$`)

// epoch is a moment in seconds, from the number the payload sends or from the
// ISO time the usage API answers with, fractional seconds and offset included.
func epoch(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(math.Floor(t)), true
	case string:
		if at, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return at.Unix(), true
		}
		if len(t) < 19 {
			return 0, false
		}
		at, err := time.Parse("2006-01-02T15:04:05", t[:19])
		if err != nil {
			return 0, false
		}
		secs := at.Unix()
		if m := offset.FindStringSubmatch(t); m != nil {
			hours, _ := strconv.Atoi(m[2])
			minutes, _ := strconv.Atoi(m[3])
			shift := int64(hours*3600 + minutes*60)
			if m[1] == "-" {
				shift = -shift
			}
			secs -= shift
		}
		return secs, true
	}
	return 0, false
}

// The readers most values are made of: one field of the payload, read as what
// the value is.

func payloadText(path ...string) func(*facts, Entry) (reading, bool) {
	return func(f *facts, _ Entry) (reading, bool) {
		text, _ := str(field(f.payload, path...))
		return textReading(text)
	}
}

func payloadNumber(format func(float64) (reading, bool), path ...string) func(*facts, Entry) (reading, bool) {
	return func(f *facts, _ Entry) (reading, bool) {
		return readNumber(format, field(f.payload, path...))
	}
}

func readNumber(format func(float64) (reading, bool), v any) (reading, bool) {
	x, ok := number(v)
	if !ok {
		return reading{}, false
	}
	return format(x)
}

// payloadMark is a value that is a word or nothing: the word while the
// payload says true there, nothing otherwise.
func payloadMark(word string, path ...string) func(*facts, Entry) (reading, bool) {
	return func(f *facts, _ Entry) (reading, bool) {
		if on, _ := field(f.payload, path...).(bool); !on {
			return reading{}, false
		}
		return textReading(word)
	}
}

func payloadMillis(path ...string) func(*facts, Entry) (reading, bool) {
	return func(f *facts, _ Entry) (reading, bool) {
		ms, ok := number(field(f.payload, path...))
		if !ok {
			return reading{}, false
		}
		return span(int64(math.Floor(ms / 1000)))
	}
}

func payloadUntil(path ...string) func(*facts, Entry) (reading, bool) {
	return func(f *facts, _ Entry) (reading, bool) {
		stamp, ok := epoch(field(f.payload, path...))
		if !ok {
			return reading{}, false
		}
		return until(stamp, f.env.Now.Unix())
	}
}

func sessionSum(pick func(tokenSums) float64) func(*facts, Entry) (reading, bool) {
	return func(f *facts, _ Entry) (reading, bool) {
		if !f.sums.counted {
			return reading{}, false
		}
		return count(pick(f.sums))
	}
}
