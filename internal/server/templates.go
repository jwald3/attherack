package server

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jwald3/attherack/internal/markdown"
	"github.com/jwald3/attherack/internal/store"
	"github.com/jwald3/attherack/web"
)

// parseTemplates loads every page and fragment template.
func parseTemplates() (*template.Template, error) {
	return template.New("").Funcs(templateFuncs()).ParseFS(web.Templates, "*.html")
}

// assetVersion is a short content hash of the embedded static assets, computed
// once at startup. It's appended to /static/ links (?v=...) so a changed CSS or
// JS file gets a new URL and browsers fetch it instead of serving a stale copy.
var assetVersion = hashStatic(web.Static)

// hashStatic returns the first 10 hex chars of a SHA-256 over every static
// file's path and contents, in sorted path order for a stable result.
func hashStatic(fsys fs.FS) string {
	h := sha256.New()
	var paths []string
	fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	for _, p := range paths {
		io.WriteString(h, p)
		if f, err := fsys.Open(p); err == nil {
			io.Copy(h, f)
			f.Close()
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

// templateFuncs returns the helper functions available in every template.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"deref":    strDeref,
		"nl2br":    nl2br,
		"md":       markdown.Render,
		"dur":      fmtDuration,
		"pace":     fmtPace,
		"hms":      fmtClock,
		"mph":      fmtSpeed,
		"pct":      percent,
		"icon":     icon,
		"imgs":     func(imgs []store.ChatImage) template.HTML { return template.HTML(imagesHTML(imgs)) },
		"add":      func(a, b int) int { return a + b },
		"assetver": func() string { return assetVersion },
		"errorbubble": func(id int64) template.HTML {
			return template.HTML(errorBubbleHTML(strconv.FormatInt(id, 10)))
		},
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
