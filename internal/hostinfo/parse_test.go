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

const procStatFixture = `cpu  10132153 290696 3084719 46828483 16683 0 25195 0 175628 0
cpu0 1393280 32966 572056 13343292 6130 0 17875 0 23933 0
cpu1 1385276 32497 564236 13361676 3175 0 4416 0 41982 0
intr 1462898 148 0 0 0
ctxt 115315133
btime 1725000000
`

func TestParseProcStat(t *testing.T) {
	idle, total, ok := parseProcStat(procStatFixture)
	if !ok {
		t.Fatal("fixture not parsed")
	}
	// idle plus iowait: waiting for a disk is not work.
	if idle != 46828483+16683 {
		t.Fatalf("idle %d", idle)
	}
	// The first eight fields alone: the guest columns are already counted in
	// user, taking them too would count that work twice.
	if total != 60377929 {
		t.Fatalf("total %d", total)
	}
	if _, _, ok := parseProcStat(""); ok {
		t.Fatal("empty input reported a sample")
	}
	if _, _, ok := parseProcStat("cpu0 1 2 3 4 5\nintr 7\n"); ok {
		t.Fatal("a file without the summary line was accepted")
	}
	if _, _, ok := parseProcStat("cpu 1 2 x 4 5\n"); ok {
		t.Fatal("garbage reported a sample")
	}
	// An old kernel prints only user, nice, system and idle.
	idle, total, ok = parseProcStat("cpu 4705 150 1120 16250\n")
	if !ok || idle != 16250 || total != 22225 {
		t.Fatalf("short line gave idle %d total %d ok=%v", idle, total, ok)
	}
}

func TestBusyPercent(t *testing.T) {
	before := "cpu 1000 0 500 8000 500 0 0 0 0 0\n"
	after := "cpu 1150 0 550 8600 500 0 0 0 0 0\n"
	idle1, total1, ok := parseProcStat(before)
	if !ok {
		t.Fatal("before not parsed")
	}
	idle2, total2, ok := parseProcStat(after)
	if !ok {
		t.Fatal("after not parsed")
	}
	// 200 of the 800 ticks in the window were work.
	if got := busyPercent(idle2-idle1, total2-total1); got != 25 {
		t.Fatalf("busy %d, want 25", got)
	}
	if busyPercent(600, 1000) != 40 {
		t.Fatal("plain delta miscounted")
	}
	if busyPercent(0, 0) != 0 {
		t.Fatal("an empty window did not answer zero")
	}
	if busyPercent(1200, 1000) != 0 {
		t.Fatal("more idle than window did not clamp to zero")
	}
}

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
