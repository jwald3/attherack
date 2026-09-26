package server

import (
	"html/template"
	"strings"

	"github.com/jwald3/attherack/internal/markdown"
	"github.com/jwald3/attherack/internal/store"
	"github.com/jwald3/attherack/web"
)

// parseTemplates loads every page and fragment template.
func parseTemplates() (*template.Template, error) {
	return template.New("").Funcs(templateFuncs()).ParseFS(web.Templates, "*.html")
}

// templateFuncs returns the helper functions available in every template.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"deref": strDeref,
		"nl2br": nl2br,
		"md":    markdown.Render,
		"dur":   fmtDuration,
		"pace":  fmtPace,
		"hms":   fmtClock,
		"mph":   fmtSpeed,
		"pct":   percent,
		"icon":  icon,
		"imgs":  func(imgs []store.ChatImage) template.HTML { return template.HTML(imagesHTML(imgs)) },
	}
}

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// nl2br escapes text and converts newlines to <br> for safe HTML display.
func nl2br(s string) template.HTML {
	return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(s), "\n", "<br>"))
}
