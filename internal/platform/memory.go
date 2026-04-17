package platform

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// ThumbnailBudget returns the number of bytes the thumbnail LRU is
// allowed to consume, per docs/system-design.md → "Memory Budget
// Detection":
//
//	min(totalSystemRAM * 0.25, 4 GB)
//	floor: 512 MB
//
// If total RAM cannot be detected on this platform, falls back to
// the floor so the app still runs instead of refusing to start.
func ThumbnailBudget() int64 {
	const (
		floor   int64 = 512 * 1024 * 1024
		ceiling int64 = 4 * 1024 * 1024 * 1024
	)
	total, ok := totalSystemRAM()
	if !ok {
		return floor
	}
	quarter := int64(total / 4)
	if quarter > ceiling {
		quarter = ceiling
	}
	if quarter < floor {
		quarter = floor
	}
	return quarter
}

// totalSystemRAM reads the host's physical memory in bytes. Only
// Linux is implemented directly; other platforms return ok=false and
// callers fall back to the budget floor. Good enough for Phase 6 —
// darwin and windows detection can come with Phase 10's ops work.
func totalSystemRAM() (uint64, bool) {
	if runtime.GOOS == "linux" {
		return readMemTotalLinux()
	}
	return 0, false
}

// readMemTotalLinux parses /proc/meminfo's "MemTotal:" line. The
// value is reported in kB; convert to bytes before returning.
func readMemTotalLinux() (uint64, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}

// FormatBytes returns a human-readable size string ("1.5 GB") for
// startup logging. Kept here so main.go doesn't grow a byte-math
// helper.
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
