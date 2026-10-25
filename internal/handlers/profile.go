package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
)

type Profile struct {
	AboutUs   string     `json:"about_us"`
	Vision    string     `json:"vision"`
	Mission   string     `json:"mission"`
	Address   string     `json:"address"`
	Phone     string     `json:"phone"`
	Email     string     `json:"email"`
	UpdatedAt *time.Time `json:"updated_at"`
}

func (h *Handler) loadProfile(ctx context.Context) (Profile, error) {
	var p Profile
	err := h.DB.QueryRow(ctx,
		`SELECT about_us, vision, mission, address, phone, email, updated_at
		 FROM academy_profile ORDER BY created_at LIMIT 1`,
	).Scan(&p.AboutUs, &p.Vision, &p.Mission, &p.Address, &p.Phone, &p.Email, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, nil // nothing saved yet: empty profile, the frontend falls back to defaults
	}
	return p, err
}

// GET /api/profile   (public)
func (h *Handler) GetProfile(c echo.Context) error {
	p, err := h.loadProfile(c.Request().Context())
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, p)
}

type profileReq struct {
	AboutUs string `json:"about_us"`
	Vision  string `json:"vision"`
	Mission string `json:"mission"`
	Address string `json:"address"`
	Phone   string `json:"phone"`
	Email   string `json:"email"`
}

func (r *profileReq) validate() error {
	for _, f := range []*string{&r.AboutUs, &r.Vision, &r.Mission, &r.Address, &r.Phone, &r.Email} {
		*f = strings.TrimSpace(*f)
	}
	tooLong := func(s string, n int) bool { return utf8.RuneCountInString(s) > n }
	switch {
	case tooLong(r.AboutUs, 5000), tooLong(r.Vision, 5000), tooLong(r.Mission, 5000):
		return invalid("about_us, vision and mission can each be at most 5000 characters")
	case tooLong(r.Address, 500):
		return invalid("address can be at most 500 characters")
	case tooLong(r.Phone, 50):
		return invalid("phone can be at most 50 characters")
	case tooLong(r.Email, 254):
		return invalid("email can be at most 254 characters")
	}
	if r.Email != "" {
		if _, err := mail.ParseAddress(r.Email); err != nil {
			return invalid("email is not valid")
		}
	}
	return nil
}

// PUT /api/admin/profile   (super_admin) - creates the single profile row on first save, updates it afterwards
func (h *Handler) UpdateProfile(c echo.Context) error {
	var r profileReq
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := r.validate(); err != nil {
		return err
	}
	const update = `UPDATE academy_profile
		SET about_us=$1, vision=$2, mission=$3, address=$4, phone=$5, email=$6, updated_at=now()
		WHERE id = (SELECT id FROM academy_profile ORDER BY created_at LIMIT 1)`
	const insert = `INSERT INTO academy_profile (about_us, vision, mission, address, phone, email)
		VALUES ($1,$2,$3,$4,$5,$6)`
	args := []any{r.AboutUs, r.Vision, r.Mission, r.Address, r.Phone, r.Email}

	ctx := c.Request().Context()
	tag, err := h.DB.Exec(ctx, update, args...)
	if err == nil && tag.RowsAffected() == 0 {
		_, err = h.DB.Exec(ctx, insert, args...)
		if isUniqueViolation(err) { // two first saves raced: the other one won, so update instead
			_, err = h.DB.Exec(ctx, update, args...)
		}
	}
	if err != nil {
		return internalErr(err)
	}
	p, err := h.loadProfile(ctx)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, p)
}
