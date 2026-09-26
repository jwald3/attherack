package server

import "fmt"

// Number formatting shared by templates and view models.

// fmtDuration renders a seconds count as "43m" or "1h 12m".
func fmtDuration(seconds int) string {
	if seconds <= 0 {
		return "—"
	}
	m := seconds / 60
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh %dm", m/60, m%60)
}

// percent returns n/total as a whole-number percentage clamped to 0-100.
func percent(n, total int) int {
	if total <= 0 || n <= 0 {
		return 0
	}
	if n >= total {
		return 100
	}
	return n * 100 / total
}

// fmtClock renders seconds as "m:ss" or "h:mm:ss".
func fmtClock(seconds int) string {
	if seconds <= 0 {
		return "—"
	}
	h, m, s := seconds/3600, (seconds%3600)/60, seconds%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// fmtPace renders a session's pace as "8:32/mi", or "" if it can't be computed.
func fmtPace(seconds int, miles float64) string {
	if seconds <= 0 || miles <= 0 {
		return ""
	}
	return fmtClock(int(float64(seconds)/miles)) + "/mi"
}

// fmtSpeed renders a session's average speed as "12.4 mph", or "".
func fmtSpeed(seconds int, miles float64) string {
	if seconds <= 0 || miles <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f mph", miles/(float64(seconds)/3600))
}
