package server

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/jwald3/attherack/web"
)

// Every {{icon "name"}} used in a template must exist in the icons map;
// otherwise the button silently renders empty.
func TestTemplateIconsExist(t *testing.T) {
	use := regexp.MustCompile(`\{\{\s*icon\s+"([^"]+)"\s*\}\}`)
	found := 0
	err := fs.WalkDir(web.Templates, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		src, err := fs.ReadFile(web.Templates, path)
		if err != nil {
			return err
		}
		for _, m := range use.FindAllStringSubmatch(string(src), -1) {
			found++
			if _, ok := icons[m[1]]; !ok {
				t.Errorf("%s uses unknown icon %q", path, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("no icon uses found; did the template syntax change?")
	}
}

func TestIconMarkup(t *testing.T) {
	got := string(icon("x"))
	if !strings.HasPrefix(got, `<svg class="icon icon-x"`) || !strings.Contains(got, `aria-hidden="true"`) {
		t.Errorf("unexpected markup: %s", got)
	}
	if icon("no-such-icon") != "" {
		t.Error("unknown icon should render nothing")
	}
}
