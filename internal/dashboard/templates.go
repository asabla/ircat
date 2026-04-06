package dashboard

import (
	"embed"
	"fmt"
	"html/template"
	"io"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// templates holds the compiled per-page templates. Each page is the
// templates/base.html layout with its own templates/<page>.html
// content block parsed into the same set, so {{template "content"}}
// resolves to the page-specific block at render time.
type templates struct {
	pages map[string]*template.Template
}

func loadTemplates() (*templates, error) {
	pages := []string{"login", "overview", "users", "channels", "operators", "events"}
	out := &templates{pages: make(map[string]*template.Template, len(pages))}
	for _, name := range pages {
		t, err := template.New("base.html").ParseFS(templateFS,
			"templates/base.html",
			"templates/"+name+".html",
		)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		out.pages[name] = t
	}
	return out, nil
}

// render writes the named page to w with data. Pages always render
// via the "base" template, which then includes the page-specific
// "content" block.
func (t *templates) render(w io.Writer, name string, data any) error {
	tpl, ok := t.pages[name]
	if !ok {
		return fmt.Errorf("unknown page %q", name)
	}
	return tpl.ExecuteTemplate(w, "base", data)
}
