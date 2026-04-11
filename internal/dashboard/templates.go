package dashboard

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"net/url"
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
	pages := []string{
		"login",
		"overview",
		"users", "user_detail",
		"channels", "channel_detail",
		"federation",
		"bots", "bot_detail",
		"operators",
		"accounts", "account_detail",
		"tokens",
		"events",
		"logs",
	}
	funcs := template.FuncMap{
		// pathEscape percent-encodes a string for safe use in URL
		// path segments. Needed because channel names start with #
		// which browsers interpret as a fragment anchor.
		"pathEscape": url.PathEscape,
	}
	out := &templates{pages: make(map[string]*template.Template, len(pages))}
	for _, name := range pages {
		t, err := template.New("base.html").Funcs(funcs).ParseFS(templateFS,
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

// renderPartial renders a single named block (rather than the
// "base" wrapper) — used by htmx fragment handlers like the
// overview cards refresh. The page name picks which template
// set to use; the block name selects the {{define}}d block.
func (t *templates) renderPartial(w io.Writer, page, block string, data any) error {
	tpl, ok := t.pages[page]
	if !ok {
		return fmt.Errorf("unknown page %q", page)
	}
	return tpl.ExecuteTemplate(w, block, data)
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
