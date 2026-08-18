package statusline

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const costDirName = "cost"

// rememberCost is the one value that needs to remember something: the cost of
// the last request is what the total went up by since the line was last
// drawn, so the last reading is kept per coder. claude draws the line after
// every request inside a turn, so the rise is one request and never a whole
// turn with tools. The rise is kept with the reading, because the line is also
// drawn without a request in between and a rise of nothing would otherwise
// wipe the number off the screen a second later. Whole millionths of a dollar
// throughout, so no reading is ever a rounding away from the one before.
func rememberCost(payload any, cacheDir string) *reading {
	key, _ := str(field(payload, "session_id"))
	usd, ok := number(field(payload, "cost", "total_cost_usd"))
	if cacheDir == "" || !ok || !safeKey(key) {
		return nil
	}
	micro := int64(math.Floor(usd * 1e6))
	dir := filepath.Join(cacheDir, costDirName)
	path := filepath.Join(dir, key)
	var seen, rise int64 = -1, -1
	if data, err := os.ReadFile(path); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			seen = digits(fields[0])
		}
		if len(fields) > 1 {
			rise = digits(fields[1])
		}
	}
	if seen >= 0 && micro > seen {
		rise = micro - seen
	}
	record := strconv.FormatInt(micro, 10) + " "
	if rise >= 0 {
		record += strconv.FormatInt(rise, 10)
	}
	if os.MkdirAll(dir, 0o700) == nil {
		_ = os.WriteFile(path, []byte(record+"\n"), 0o600)
	}
	if rise < 0 {
		return nil
	}
	// Rounded to the cent, the way every amount on the line is, so two amounts
	// are never a rounding apart. The cents are also exactly what a bound
	// compares against, a dollar times a hundred.
	cents := (rise + 5000) / 10000
	return &reading{text: "$" + dollars(cents), scaled: cents}
}

// safeKey keeps the coder's id to what a file name can carry without becoming
// a path: letters, digits and the dash of a uuid.
func safeKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
			return false
		}
	}
	return true
}

// digits reads a whole number that is nothing but digits, -1 for anything else.
func digits(text string) int64 {
	if text == "" || strings.Trim(text, "0123456789") != "" {
		return -1
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return -1
	}
	return n
}
