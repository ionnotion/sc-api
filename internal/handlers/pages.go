package handlers

import (
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"schoolapp/web"
)

// Renderer implements echo.Renderer: each page = layout.html + <page>.html.
type Renderer struct{ tpls map[string]*template.Template }

func NewRenderer() (*Renderer, error) {
	funcs := template.FuncMap{
		"date": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("2 January 2006")
		},
		"paragraphs": func(s string) []string {
			var out []string
			for _, p := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n\n") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			return out
		},
	}
	r := &Renderer{tpls: map[string]*template.Template{}}
	for _, name := range []string{"home", "about", "contact", "news_list", "news_detail"} {
		t, err := template.New("").Funcs(funcs).ParseFS(web.FS, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		r.tpls[name] = t
	}
	return r, nil
}

func (r *Renderer) Render(w io.Writer, name string, data any, _ echo.Context) error {
	t, ok := r.tpls[name]
	if !ok {
		return fmt.Errorf("template %q not found", name)
	}
	return t.ExecuteTemplate(w, "layout", data)
}

func (h *Handler) render(c echo.Context, tpl, title string, data echo.Map) error {
	if data == nil {
		data = echo.Map{}
	}
	data["Title"] = title
	data["Site"] = h.Cfg.Site
	data["Year"] = time.Now().Year()
	return c.Render(http.StatusOK, tpl, data)
}

func (h *Handler) HomePage(c echo.Context) error {
	posts, err := h.queryPosts(c.Request().Context(), true, "", 3, 0)
	if err != nil {
		return internalErr(err)
	}
	return h.render(c, "home", "Home", echo.Map{"Posts": posts})
}

func (h *Handler) AboutPage(c echo.Context) error   { return h.render(c, "about", "About us", nil) }
func (h *Handler) ContactPage(c echo.Context) error { return h.render(c, "contact", "Contact", nil) }

func (h *Handler) NewsPage(c echo.Context) error {
	typ, err := typeFilter(c)
	if err != nil {
		return err
	}
	const perPage = 10
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}
	posts, err := h.queryPosts(c.Request().Context(), true, typ, perPage+1, (page-1)*perPage)
	if err != nil {
		return internalErr(err)
	}
	hasNext := len(posts) > perPage
	if hasNext {
		posts = posts[:perPage]
	}
	return h.render(c, "news_list", "News & announcements", echo.Map{
		"Posts": posts, "Type": typ, "HasPrev": page > 1, "Prev": page - 1, "HasNext": hasNext, "Next": page + 1,
	})
}

func (h *Handler) NewsDetailPage(c echo.Context) error {
	p, err := h.postBySlug(c.Request().Context(), c.Param("slug"))
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("post")
	}
	if err != nil {
		return internalErr(err)
	}
	return h.render(c, "news_detail", p.Title, echo.Map{"Post": p})
}
