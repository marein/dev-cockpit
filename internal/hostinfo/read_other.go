//go:build !linux && !darwin

package hostinfo

// Anything that is neither Linux nor macOS reports nothing, and the surfaces
// leave the status out rather than showing zeroes.

func loadAverage() (float64, bool) { return 0, false }

func memory() (total, available uint64, ok bool) { return 0, 0, false }

func disk(string) (total, free uint64, ok bool) { return 0, 0, false }
