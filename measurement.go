package main

// MeasurementSite is one tracked body-measurement location.
type MeasurementSite struct {
	Slug  string
	Label string
}

// measurementSites is the fixed set of body measurements the app tracks. The
// form, the coach tool's enum, and history rendering all read from this list,
// so adding a site here surfaces it everywhere.
var measurementSites = []MeasurementSite{
	{"waist", "Waist"},
	{"chest", "Chest"},
	{"hips", "Hips"},
	{"neck", "Neck"},
	{"arm_l", "Left arm"},
	{"arm_r", "Right arm"},
	{"thigh_l", "Left thigh"},
	{"thigh_r", "Right thigh"},
	{"calf_l", "Left calf"},
	{"calf_r", "Right calf"},
}

// measurementSiteSlugs returns just the slugs, for the coach tool's enum.
func measurementSiteSlugs() []string {
	out := make([]string, len(measurementSites))
	for i, s := range measurementSites {
		out[i] = s.Slug
	}
	return out
}

// measurementLabel returns the display label for a slug, or the slug itself if
// it isn't a known site.
func measurementLabel(slug string) string {
	for _, s := range measurementSites {
		if s.Slug == slug {
			return s.Label
		}
	}
	return slug
}

// isMeasurementSite reports whether slug is a known measurement site.
func isMeasurementSite(slug string) bool {
	for _, s := range measurementSites {
		if s.Slug == slug {
			return true
		}
	}
	return false
}
