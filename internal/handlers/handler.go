package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"schoolapp/internal/config"
)

type Handler struct {
	DB  *pgxpool.Pool
	Cfg config.Config
}

type scanner interface{ Scan(dest ...any) error }

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func invalid(msg string) error  { return echo.NewHTTPError(http.StatusUnprocessableEntity, msg) }
func notFound(what string) error { return echo.NewHTTPError(http.StatusNotFound, what+" not found") }
func internalErr(err error) error {
	return echo.NewHTTPError(http.StatusInternalServerError, "internal error").SetInternal(err)
}

func idParam(c echo.Context, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param(name))
	if err != nil {
		return uuid.Nil, echo.NewHTTPError(http.StatusBadRequest, "invalid id")
	}
	return id, nil
}

func paging(c echo.Context) (limit, offset int) {
	limit, _ = strconv.Atoi(c.QueryParam("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}
	return limit, (page - 1) * limit
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 80 {
		s = strings.Trim(s[:80], "-")
	}
	if s == "" {
		s = "post"
	}
	return s
}
