package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"schoolapp/internal/authmw"
)

type Post struct {
	ID          uuid.UUID  `json:"id"`
	Type        string     `json:"type"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Excerpt     string     `json:"excerpt"`
	Body        string     `json:"body"`
	IsPublished bool       `json:"is_published"`
	PublishedAt *time.Time `json:"published_at"`
	Author      string     `json:"author"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

const postSelect = `SELECT p.id, p.type::text, p.title, p.slug, p.excerpt, p.body, p.is_published,
	p.published_at, u.full_name, p.created_at, p.updated_at
	FROM posts p JOIN users u ON u.id = p.author_id`

func scanPost(s scanner, p *Post) error {
	return s.Scan(&p.ID, &p.Type, &p.Title, &p.Slug, &p.Excerpt, &p.Body, &p.IsPublished,
		&p.PublishedAt, &p.Author, &p.CreatedAt, &p.UpdatedAt)
}

func validPostType(t string) bool { return t == "news" || t == "announcement" }

func (h *Handler) queryPosts(ctx context.Context, onlyPublished bool, typ string, limit, offset int) ([]Post, error) {
	where := []string{"TRUE"}
	args := []any{}
	if onlyPublished {
		where = append(where, "p.is_published")
	}
	if typ != "" {
		args = append(args, typ)
		where = append(where, fmt.Sprintf("p.type = $%d::post_type", len(args)))
	}
	args = append(args, limit, offset)
	q := postSelect + " WHERE " + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY COALESCE(p.published_at, p.created_at) DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := h.DB.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Post{}
	for rows.Next() {
		var p Post
		if err := scanPost(rows, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (h *Handler) postBySlug(ctx context.Context, slug string) (Post, error) {
	var p Post
	err := scanPost(h.DB.QueryRow(ctx, postSelect+` WHERE p.slug=$1 AND p.is_published`, slug), &p)
	return p, err
}

func (h *Handler) postByID(ctx context.Context, id uuid.UUID) (Post, error) {
	var p Post
	err := scanPost(h.DB.QueryRow(ctx, postSelect+` WHERE p.id=$1`, id), &p)
	return p, err
}

func typeFilter(c echo.Context) (string, error) {
	t := c.QueryParam("type")
	if t != "" && !validPostType(t) {
		return "", invalid("type must be news or announcement")
	}
	return t, nil
}

// GET /api/news?type=&page=&limit=   (public, published only)
func (h *Handler) ListNewsAPI(c echo.Context) error {
	typ, err := typeFilter(c)
	if err != nil {
		return err
	}
	limit, offset := paging(c)
	posts, err := h.queryPosts(c.Request().Context(), true, typ, limit, offset)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, posts)
}

// GET /api/news/:slug   (public)
func (h *Handler) GetNewsAPI(c echo.Context) error {
	p, err := h.postBySlug(c.Request().Context(), c.Param("slug"))
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("post")
	}
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, p)
}

// GET /api/admin/news   (news_admin, super_admin) - includes drafts
func (h *Handler) ListNewsAdmin(c echo.Context) error {
	typ, err := typeFilter(c)
	if err != nil {
		return err
	}
	limit, offset := paging(c)
	posts, err := h.queryPosts(c.Request().Context(), false, typ, limit, offset)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, posts)
}

type postReq struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Excerpt     string `json:"excerpt"`
	Body        string `json:"body"`
	IsPublished bool   `json:"is_published"`
}

func (r *postReq) validate() error {
	r.Title = strings.TrimSpace(r.Title)
	r.Body = strings.TrimSpace(r.Body)
	if !validPostType(r.Type) {
		return invalid("type must be news or announcement")
	}
	if r.Title == "" || r.Body == "" {
		return invalid("title and body are required")
	}
	return nil
}

// POST /api/admin/news
func (h *Handler) CreatePost(c echo.Context) error {
	var r postReq
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := r.validate(); err != nil {
		return err
	}
	ctx := c.Request().Context()
	base := slugify(r.Title)
	var id uuid.UUID
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		slug := base
		if attempt > 0 {
			slug += "-" + uuid.NewString()[:6]
		}
		err = h.DB.QueryRow(ctx,
			`INSERT INTO posts (type, title, slug, excerpt, body, is_published, published_at, author_id)
			 VALUES ($1, $2, $3, $4, $5, $6, CASE WHEN $6 THEN now() END, $7) RETURNING id`,
			r.Type, r.Title, slug, r.Excerpt, r.Body, r.IsPublished, authmw.UserID(c)).Scan(&id)
		if !isUniqueViolation(err) {
			break
		}
	}
	if err != nil {
		return internalErr(err)
	}
	p, err := h.postByID(ctx, id)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusCreated, p)
}

// PUT /api/admin/news/:id   (slug stays stable so public links don't break)
func (h *Handler) UpdatePost(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	var r postReq
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := r.validate(); err != nil {
		return err
	}
	ctx := c.Request().Context()
	tag, err := h.DB.Exec(ctx,
		`UPDATE posts SET type=$2, title=$3, excerpt=$4, body=$5, is_published=$6,
		 published_at = CASE WHEN $6 THEN COALESCE(published_at, now()) END, updated_at = now()
		 WHERE id=$1`, id, r.Type, r.Title, r.Excerpt, r.Body, r.IsPublished)
	if err != nil {
		return internalErr(err)
	}
	if tag.RowsAffected() == 0 {
		return notFound("post")
	}
	p, err := h.postByID(ctx, id)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, p)
}

// DELETE /api/admin/news/:id
func (h *Handler) DeletePost(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	tag, err := h.DB.Exec(c.Request().Context(), `DELETE FROM posts WHERE id=$1`, id)
	if err != nil {
		return internalErr(err)
	}
	if tag.RowsAffected() == 0 {
		return notFound("post")
	}
	return c.NoContent(http.StatusNoContent)
}

// GET /api/admin/news/:id   (includes drafts)
func (h *Handler) GetPostAdmin(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	p, err := h.postByID(c.Request().Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("post")
	}
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, p)
}
