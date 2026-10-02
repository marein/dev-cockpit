package cost

import "time"

// buildReport is the report of a named range up to now, the way the page
// asks for one.
func buildReport(rows []Row, now time.Time, rangeName string) Report {
	from, _ := RangeSpan(rangeName, now)
	return build(rows, now, Span{From: from})
}
