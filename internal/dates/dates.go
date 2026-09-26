// Package dates holds the app's calendar helpers. Every date the app stores or
// compares is a local-time YYYY-MM-DD string, so these return that form.
package dates

import "time"

// Layout is the YYYY-MM-DD format used for every stored date.
const Layout = "2006-01-02"

// Today returns the current local date as YYYY-MM-DD.
func Today() string { return time.Now().Format(Layout) }

// DaysAgo returns the date N days before today as YYYY-MM-DD.
func DaysAgo(n int) string {
	return time.Now().AddDate(0, 0, -n).Format(Layout)
}
