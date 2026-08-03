package hostinfo

import "testing"

// The macOS fixtures are the reason these parsers are pure: this project is
// developed on Linux, so the only way to cover the darwin path is to feed it
// the output the real tools print.

const meminfoFixture = `MemTotal:       16314516 kB
MemFree:         1180416 kB
MemAvailable:    9887432 kB
Buffers:          412300 kB
Cached:          6820164 kB
SwapTotal:       2097148 kB
SwapFree:        2097148 kB
`

const vmStatFixture = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                              123456.
Pages active:                            456789.
Pages inactive:                          234567.
Pages speculative:                        12345.
Pages throttled:                              0.
Pages wired down:                        345678.
Pages purgeable:                           6789.
"Translation faults":                  987654321.
`

func TestParseProcLoadAvg(t *testing.T) {
	got, ok := parseProcLoadAvg("0.52 0.58 0.59 1/834 12345")
	if !ok || got != 0.52 {
		t.Fatalf("got %v ok=%v, want 0.52 true", got, ok)
	}
	if _, ok := parseProcLoadAvg(""); ok {
		t.Fatal("empty input reported a load")
	}
	if _, ok := parseProcLoadAvg("not-a-number 1 2"); ok {
		t.Fatal("garbage reported a load")
	}
}

func TestParseSysctlLoadAvg(t *testing.T) {
	got, ok := parseSysctlLoadAvg("{ 1.85 2.03 2.11 }\n")
	if !ok || got != 1.85 {
		t.Fatalf("got %v ok=%v, want 1.85 true", got, ok)
	}
	if _, ok := parseSysctlLoadAvg("{ }"); ok {
		t.Fatal("empty braces reported a load")
	}
}

func TestParseMeminfo(t *testing.T) {
	total, available, ok := parseMeminfo(meminfoFixture)
	if !ok {
		t.Fatal("fixture not parsed")
	}
	if total != 16314516*1024 {
		t.Fatalf("total %d", total)
	}
	// MemAvailable, not MemFree: the page cache is not in use.
	if available != 9887432*1024 {
		t.Fatalf("available %d", available)
	}
	if _, _, ok := parseMeminfo("MemTotal: 16 kB\n"); ok {
		t.Fatal("a file without MemAvailable was accepted")
	}
}

func TestParseVMStat(t *testing.T) {
	available, ok := parseVMStat(vmStatFixture)
	if !ok {
		t.Fatal("fixture not parsed")
	}
	// free + inactive + speculative + purgeable, at the page size the header names.
	want := uint64(123456+234567+12345+6789) * 16384
	if available != want {
		t.Fatalf("available %d, want %d", available, want)
	}
	if _, ok := parseVMStat("Mach Virtual Memory Statistics: (page size of 4096 bytes)\n"); ok {
		t.Fatal("output without a free count was accepted")
	}
}

func TestParseSysctlUint(t *testing.T) {
	got, ok := parseSysctlUint("17179869184\n")
	if !ok || got != 17179869184 {
		t.Fatalf("got %d ok=%v", got, ok)
	}
	if _, ok := parseSysctlUint("{ 1.85 }"); ok {
		t.Fatal("garbage accepted")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KB"},
		{5<<30 + 800<<20, "5.8 GB"},
		{16 << 30, "16 GB"},
		{2 << 40, "2.0 TB"},
		// hw.memsize of a 16 GB Mac, so the reading matches what the box is sold as.
		{17179869184, "16 GB"},
	}
	for _, tc := range cases {
		if got := humanBytes(tc.in); got != tc.want {
			t.Fatalf("humanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStatsLevelAndWorst(t *testing.T) {
	s := Stats{HasCPU: true, CPUPercent: 12, HasMem: true, MemPercent: 84, HasDisk: true, DiskPercent: 40}
	if s.Worst() != 84 {
		t.Fatalf("worst %d", s.Worst())
	}
	if Bar(140) != 100 || Bar(-1) != 0 || Bar(40) != 40 {
		t.Fatal("bar did not clamp")
	}
	for _, tc := range []struct {
		worst int
		level string
	}{{10, ""}, {79, ""}, {80, "warn"}, {94, "warn"}, {95, "crit"}, {140, "crit"}} {
		s := Stats{HasCPU: true, CPUPercent: tc.worst}
		got := ""
		switch {
		case s.Worst() >= Crit:
			got = "crit"
		case s.Worst() >= Warn:
			got = "warn"
		}
		if got != tc.level {
			t.Fatalf("worst %d gave %q, want %q", tc.worst, got, tc.level)
		}
	}
}
