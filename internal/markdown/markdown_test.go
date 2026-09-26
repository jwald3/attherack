package markdown

import (
	"strings"
	"testing"
)

func TestRenderMarkdownTable(t *testing.T) {
	src := `Race plan:

| Segment | Pace | Time | Feel |
|---|:---:|---:|---|
| Mile 1 | 8:35 | ~8:35 | Controlled. Should feel "too easy." |
| Mile 2 | 8:25 | ~17:00 | **Settle in** |
| Pipe \| test | 1 |

After the table.`
	got := string(Render(src))

	for _, want := range []string{
		`<p>Race plan:</p>`,
		`<div class="md-table"><table><thead><tr><th>Segment</th><th style="text-align:center">Pace</th><th style="text-align:right">Time</th><th>Feel</th></tr></thead>`,
		`<td>Mile 1</td><td style="text-align:center">8:35</td><td style="text-align:right">~8:35</td><td>Controlled. Should feel &#34;too easy.&#34;</td>`,
		`<td><strong>Settle in</strong></td>`,
		`<td>Pipe | test</td><td style="text-align:center">1</td><td style="text-align:right"></td><td></td>`, // short row padded
		`</tbody></table></div><p>After the table.</p>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s\n\ngot: %s", want, got)
		}
	}
}

func TestRenderMarkdownNoFalseTables(t *testing.T) {
	// A pipe in prose with no separator row stays a paragraph.
	got := string(Render("Squat | Bench | Deadlift\nthat's the big three"))
	if strings.Contains(got, "<table>") {
		t.Errorf("unexpected table: %s", got)
	}
	// A lone --- is a rule, not a table separator.
	if got := string(Render("above\n\n---\n\nbelow")); !strings.Contains(got, "<hr>") {
		t.Errorf("expected <hr>: %s", got)
	}
}

func TestRenderMarkdownEscapesTableCells(t *testing.T) {
	got := string(Render("| a |\n|---|\n| <script>x</script> |"))
	if strings.Contains(got, "<script>") {
		t.Errorf("cell not escaped: %s", got)
	}
}
