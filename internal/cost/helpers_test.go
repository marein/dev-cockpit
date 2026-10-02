package cost

import "time"

// buildReport is the report of a named range up to now, the way the page
// asks for one.
func buildReport(rows []Row, now time.Time, rangeName string) Report {
	from, to := RangeSpan(rangeName, now)
	return build(rows, now, Span{From: from, To: to, Step: StepDay})
}

// total is the spend of a report's span, every project and the assistants.
func total(r Report) float64 {
	sum := 0.0
	for _, p := range r.Projects {
		sum += p.USD
	}
	return sum
}
