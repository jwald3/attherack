// Package web embeds the HTML templates and static files, so the binary is
// self-contained. Edit the files under templates/ and static/, then restart.
package web

import (
	"embed"
	"io/fs"
)

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed static/*
var staticFiles embed.FS

// Templates holds the page and fragment templates (*.html at its root).
var Templates = mustSub(templateFiles, "templates")

// Static holds the files served under /static/.
var Static = mustSub(staticFiles, "static")

func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
