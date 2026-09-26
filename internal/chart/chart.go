// Package chart renders small server-side SVG line charts. Pure Go, no JS,
// CSP-safe.
package chart

import (
	"fmt"
	"html/template"
	"strings"
)

// Data holds a rendered line chart plus summary stats of its series.
type Data struct {
	HasData bool
	SVG     template.HTML
	First   float64
	Last    float64
	Min     float64
	Max     float64
	Change  float64
	Count   int
}

// Point is a single (date, value) sample.
type Point struct {
	Date  string
	Value float64
}

// Build renders an SVG line chart from points (any order). It plots value over
// time with a light area fill, min/max gridlines, and end-point labels.
func Build(in []Point) Data {
	return BuildFmt(in, func(v float64) string { return fmt.Sprintf("%g", roundish(v)) })
}

// BuildFmt is Build with a custom y-axis label formatter (e.g. rendering
// seconds-per-mile as "8:53").
func BuildFmt(in []Point, yLabel func(float64) string) Data {
	if len(in) == 0 {
		return Data{}
	}

	// Sort ascending by date.
	pts := make([]Point, len(in))
	copy(pts, in)
	sortPointsByDateAsc(pts)

	minW, maxW := pts[0].Value, pts[0].Value
	for _, p := range pts {
		if p.Value < minW {
			minW = p.Value
		}
		if p.Value > maxW {
			maxW = p.Value
		}
	}

	cd := Data{
		HasData: true,
		First:   pts[0].Value,
		Last:    pts[len(pts)-1].Value,
		Min:     minW,
		Max:     maxW,
		Change:  pts[len(pts)-1].Value - pts[0].Value,
		Count:   len(pts),
	}

	// Geometry.
	const (
		w, h                   = 720.0, 300.0
		padL, padR, padT, padB = 44.0, 16.0, 18.0, 28.0
	)
	plotW := w - padL - padR
	plotH := h - padT - padB

	// Y range with a little headroom; guard against a flat series.
	lo, hi := minW, maxW
	if hi-lo < 1 {
		lo -= 1
		hi += 1
	}
	span := hi - lo
	pad := span * 0.08
	lo -= pad
	hi += pad
	span = hi - lo

	// X positions: evenly spaced by index (dates aren't guaranteed regular, but
	// even spacing reads cleanly for a log with irregular gaps).
	xAt := func(i int) float64 {
		if len(pts) == 1 {
			return padL + plotW/2
		}
		return padL + plotW*float64(i)/float64(len(pts)-1)
	}
	yAt := func(v float64) float64 {
		return padT + plotH*(1-(v-lo)/span)
	}

	var line strings.Builder
	var area strings.Builder
	for i, p := range pts {
		x, y := xAt(i), yAt(p.Value)
		if i == 0 {
			fmt.Fprintf(&line, "M%.1f %.1f", x, y)
			fmt.Fprintf(&area, "M%.1f %.1f", x, padT+plotH)
			fmt.Fprintf(&area, " L%.1f %.1f", x, y)
		} else {
			fmt.Fprintf(&line, " L%.1f %.1f", x, y)
			fmt.Fprintf(&area, " L%.1f %.1f", x, y)
		}
	}
	fmt.Fprintf(&area, " L%.1f %.1f Z", xAt(len(pts)-1), padT+plotH)

	// Gridlines + labels at min, mid, max of the actual data.
	grid := func(v float64) string {
		y := yAt(v)
		return fmt.Sprintf(
			`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" class="grid"/>`+
				`<text x="%.1f" y="%.1f" class="axis-y">%s</text>`,
			padL, y, w-padR, y, padL-6, y+3, template.HTMLEscapeString(yLabel(v)))
	}
	mid := (minW + maxW) / 2

	// End-point dot + label.
	lastX, lastY := xAt(len(pts)-1), yAt(cd.Last)
	firstDate := pts[0].Date
	lastDate := pts[len(pts)-1].Date

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" class="bw-svg" preserveAspectRatio="none" role="img" aria-label="Bodyweight over time">`, w, h)
	b.WriteString(grid(maxW))
	b.WriteString(grid(mid))
	b.WriteString(grid(minW))
	fmt.Fprintf(&b, `<path d="%s" class="area"/>`, area.String())
	fmt.Fprintf(&b, `<path d="%s" class="line"/>`, line.String())
	fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="3.5" class="dot"/>`, lastX, lastY)
	// X-axis end labels (first & last date).
	fmt.Fprintf(&b, `<text x="%.1f" y="%.0f" class="axis-x start">%s</text>`, padL, h-8, firstDate)
	fmt.Fprintf(&b, `<text x="%.1f" y="%.0f" class="axis-x end">%s</text>`, w-padR, h-8, lastDate)
	b.WriteString(`</svg>`)

	cd.SVG = template.HTML(b.String())
	return cd
}

// sortPointsByDateAsc sorts points ascending by their YYYY-MM-DD date string.
func sortPointsByDateAsc(pts []Point) {
	// Simple insertion sort — lists are small (≤ a few hundred).
	for i := 1; i < len(pts); i++ {
		for j := i; j > 0 && pts[j-1].Date > pts[j].Date; j-- {
			pts[j-1], pts[j] = pts[j], pts[j-1]
		}
	}
}

// roundish trims a value to one decimal for axis labels.
func roundish(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}
