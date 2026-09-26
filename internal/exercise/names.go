package exercise

import "strings"

// CustomID returns the stable id for a user-added exercise with this name.
func CustomID(name string) string {
	return "custom_" + slugify(name)
}

// slugify turns an exercise name into a stable id fragment (letters/digits/_).
func slugify(name string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

// SplitList / JoinList (de)serialize muscle lists stored or submitted as
// comma-separated text.
func SplitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	return CleanList(parts)
}

func JoinList(items []string) string {
	return strings.Join(CleanList(items), ",")
}

// CleanList trims each item and drops empties.
func CleanList(items []string) []string {
	var out []string
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it != "" {
			out = append(out, it)
		}
	}
	return out
}
