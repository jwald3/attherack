// Package markdown renders the small subset of Markdown the coach emits.
package markdown

import (
	"html"
	"html/template"
	"regexp"
	"strings"
)

// Render converts the small subset of Markdown that the coach emits into safe
// HTML. It is intentionally minimal (no dependencies): headings, bullet and
// numbered lists, GFM tables, horizontal rules, bold/italic/code inline spans,
// and paragraphs. All text is HTML-escaped first, so the output is
// injection-safe.
func Render(src string) template.HTML {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")

	var b strings.Builder
	var para []string // buffered paragraph lines
	listType := ""    // "ul" | "ol" | ""

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		b.WriteString("<p>")
		b.WriteString(inline(strings.Join(para, " ")))
		b.WriteString("</p>")
		para = para[:0]
	}
	closeList := func() {
		if listType != "" {
			b.WriteString("</" + listType + ">")
			listType = ""
		}
	}

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		// Blank line: end paragraph and list.
		if trimmed == "" {
			flushPara()
			closeList()
			continue
		}

		// Table: a header row followed by a |---|---| separator row.
		if strings.Contains(trimmed, "|") && i+1 < len(lines) && tableSepRe.MatchString(strings.TrimSpace(lines[i+1])) {
			flushPara()
			closeList()
			header := splitRow(trimmed)
			aligns := tableAligns(strings.TrimSpace(lines[i+1]))
			i += 2
			var rows [][]string
			for ; i < len(lines); i++ {
				row := strings.TrimSpace(lines[i])
				if row == "" || !strings.Contains(row, "|") {
					break
				}
				rows = append(rows, splitRow(row))
			}
			i-- // the loop's i++ moves to the line that ended the table
			writeTable(&b, header, aligns, rows)
			continue
		}

		// Horizontal rule: ---, ***, ___
		if hrRe.MatchString(trimmed) {
			flushPara()
			closeList()
			b.WriteString("<hr>")
			continue
		}

		// Headings: #, ##, ### -> h4/h5/h6 (kept small to fit the bubble).
		if m := headingRe.FindStringSubmatch(trimmed); m != nil {
			flushPara()
			closeList()
			level := len(m[1])
			tag := "h6"
			switch level {
			case 1:
				tag = "h4"
			case 2:
				tag = "h5"
			}
			b.WriteString("<" + tag + ">")
			b.WriteString(inline(m[2]))
			b.WriteString("</" + tag + ">")
			continue
		}

		// Bullet list item: - or *
		if m := bulletRe.FindStringSubmatch(trimmed); m != nil {
			flushPara()
			if listType != "ul" {
				closeList()
				b.WriteString("<ul>")
				listType = "ul"
			}
			b.WriteString("<li>" + inline(m[1]) + "</li>")
			continue
		}

		// Numbered list item: 1. 2) etc.
		if m := numberRe.FindStringSubmatch(trimmed); m != nil {
			flushPara()
			if listType != "ol" {
				closeList()
				b.WriteString("<ol>")
				listType = "ol"
			}
			b.WriteString("<li>" + inline(m[1]) + "</li>")
			continue
		}

		// Otherwise accumulate into the current paragraph.
		closeList()
		para = append(para, trimmed)
	}
	flushPara()
	closeList()

	return template.HTML(b.String())
}

var (
	headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	bulletRe  = regexp.MustCompile(`^[-*]\s+(.*)$`)
	numberRe  = regexp.MustCompile(`^\d+[.)]\s+(.*)$`)
	hrRe      = regexp.MustCompile(`^(?:-{3,}|\*{3,}|_{3,})$`)

	// A GFM table separator row, e.g. |---|:--:|---:| (outer pipes optional).
	tableSepRe = regexp.MustCompile(`^\|?\s*:?-{3,}:?\s*(?:\|\s*:?-{3,}:?\s*)*\|?$`)

	// Inline spans, applied after escaping. Order matters: code first so its
	// contents aren't further processed, then bold before italic.
	codeRe   = regexp.MustCompile("`([^`]+)`")
	boldRe   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italicRe = regexp.MustCompile(`\*([^*]+)\*`)
)

// inline escapes text and applies inline Markdown spans.
func inline(s string) string {
	s = html.EscapeString(s)
	// `code`
	s = codeRe.ReplaceAllString(s, "<code>$1</code>")
	// **bold**
	s = boldRe.ReplaceAllString(s, "<strong>$1</strong>")
	// *italic* (single asterisks that remain)
	s = italicRe.ReplaceAllString(s, "<em>$1</em>")
	return s
}

// splitRow splits a table row into trimmed cells, ignoring the optional outer
// pipes and treating \| as a literal pipe inside a cell.
func splitRow(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	if strings.HasSuffix(row, "|") && !strings.HasSuffix(row, `\|`) {
		row = strings.TrimSuffix(row, "|")
	}
	const esc = "\uE000"
	row = strings.ReplaceAll(row, `\|`, esc)
	cells := strings.Split(row, "|")
	for i, c := range cells {
		cells[i] = strings.TrimSpace(strings.ReplaceAll(c, esc, "|"))
	}
	return cells
}

// tableAligns reads each column's alignment from the separator row.
func tableAligns(sep string) []string {
	cells := splitRow(sep)
	out := make([]string, len(cells))
	for i, c := range cells {
		left, right := strings.HasPrefix(c, ":"), strings.HasSuffix(c, ":")
		switch {
		case left && right:
			out[i] = "center"
		case right:
			out[i] = "right"
		case left:
			out[i] = "left"
		}
	}
	return out
}

// writeTable emits a table; rows are padded or trimmed to the header's width.
func writeTable(b *strings.Builder, header, aligns []string, rows [][]string) {
	cell := func(tag string, col int, text string) {
		b.WriteString("<" + tag)
		if col < len(aligns) && aligns[col] != "" {
			b.WriteString(` style="text-align:` + aligns[col] + `"`)
		}
		b.WriteString(">" + inline(text) + "</" + tag + ">")
	}
	b.WriteString(`<div class="md-table"><table><thead><tr>`)
	for i, h := range header {
		cell("th", i, h)
	}
	b.WriteString("</tr></thead><tbody>")
	for _, r := range rows {
		b.WriteString("<tr>")
		for i := range header {
			text := ""
			if i < len(r) {
				text = r[i]
			}
			cell("td", i, text)
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table></div>")
}
