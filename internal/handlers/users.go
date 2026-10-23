package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"schoolapp/internal/auth"
	"schoolapp/internal/authmw"
)

const tokenTTL = 12 * time.Hour

var allRoles = []string{authmw.RoleUser, authmw.RoleNewsAdmin, authmw.RoleEnrollmentAdmin, authmw.RoleSuperAdmin}
var adminRoles = allRoles[1:]

type User struct {
	ID        uuid.UUID `json:"id"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Email     string    `json:"email"`
	Phone     string    `json:"phone"`
	Role      string    `json:"role"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

const userCols = `id, first_name, last_name, email, phone, role::text, is_active, created_at`

func scanUser(s scanner, u *User) error {
	return s.Scan(&u.ID, &u.FirstName, &u.LastName, &u.Email, &u.Phone, &u.Role, &u.IsActive, &u.CreatedAt)
}

type accountReq struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	Password  string `json:"password"`
	Role      string `json:"role"`
}

func (r *accountReq) validate() error {
	r.FirstName = strings.TrimSpace(r.FirstName)
	r.LastName = strings.TrimSpace(r.LastName)
	r.Email = strings.ToLower(strings.TrimSpace(r.Email))
	r.Phone = strings.TrimSpace(r.Phone)
	if r.FirstName == "" {
		return invalid("first_name is required")
	}
	if _, err := mail.ParseAddress(r.Email); err != nil || len(r.Email) > 254 {
		return invalid("a valid email is required")
	}
	if len(r.Password) < 8 || len(r.Password) > 72 {
		return invalid("password must be 8 to 72 characters")
	}
	return nil
}

func (h *Handler) insertUser(ctx context.Context, r accountReq, role string) (User, error) {
	hash, err := auth.HashPassword(r.Password)
	if err != nil {
		return User{}, err
	}
	var u User
	err = scanUser(h.DB.QueryRow(ctx,
		`INSERT INTO users (first_name, last_name, email, phone, password_hash, role) VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING `+userCols, r.FirstName, r.LastName, r.Email, r.Phone, hash, role), &u)
	return u, err
}

// LookupUser is used by the JWT middleware on every authenticated request.
func (h *Handler) LookupUser(ctx context.Context, id uuid.UUID) (string, bool, error) {
	var role string
	var active bool
	err := h.DB.QueryRow(ctx, `SELECT role::text, is_active FROM users WHERE id=$1`, id).Scan(&role, &active)
	return role, active, err
}

func (h *Handler) authResponse(u User) (echo.Map, error) {
	tok, err := auth.NewToken(u.ID.String(), h.Cfg.JWTSecret, tokenTTL)
	if err != nil {
		return nil, err
	}
	return echo.Map{"token": tok, "user": u}, nil
}

// POST /api/auth/register  (public; always creates role "user")
func (h *Handler) Register(c echo.Context) error {
	var r accountReq
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := r.validate(); err != nil {
		return err
	}
	u, err := h.insertUser(c.Request().Context(), r, authmw.RoleUser)
	if isUniqueViolation(err) {
		return echo.NewHTTPError(http.StatusConflict, "email already registered")
	}
	if err != nil {
		return internalErr(err)
	}
	resp, err := h.authResponse(u)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusCreated, resp)
}

// POST /api/auth/login
func (h *Handler) Login(c echo.Context) error {
	var r struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	var u User
	var hash string
	err := h.DB.QueryRow(c.Request().Context(),
		`SELECT `+userCols+`, password_hash FROM users WHERE email=$1`,
		strings.ToLower(strings.TrimSpace(r.Email)),
	).Scan(&u.ID, &u.FirstName, &u.LastName, &u.Email, &u.Phone, &u.Role, &u.IsActive, &u.CreatedAt, &hash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return internalErr(err)
	}
	if err != nil || !u.IsActive || !auth.CheckPassword(hash, r.Password) {
		return echo.NewHTTPError(http.StatusUnauthorized, "invalid email or password")
	}
	resp, err := h.authResponse(u)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, resp)
}

// GET /api/me
func (h *Handler) Me(c echo.Context) error {
	var u User
	err := scanUser(h.DB.QueryRow(c.Request().Context(),
		`SELECT `+userCols+` FROM users WHERE id=$1`, authmw.UserID(c)), &u)
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, u)
}

// POST /api/admin/users  (super_admin) - register an admin account
func (h *Handler) CreateAdmin(c echo.Context) error {
	var r accountReq
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := r.validate(); err != nil {
		return err
	}
	if !slices.Contains(adminRoles, r.Role) {
		return invalid("role must be one of: " + strings.Join(adminRoles, ", "))
	}
	u, err := h.insertUser(c.Request().Context(), r, r.Role)
	if isUniqueViolation(err) {
		return echo.NewHTTPError(http.StatusConflict, "email already registered")
	}
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusCreated, u)
}

// GET /api/admin/users?role=&page=&limit=  (super_admin)
func (h *Handler) ListUsers(c echo.Context) error {
	limit, offset := paging(c)
	q := `SELECT ` + userCols + ` FROM users`
	args := []any{}
	if role := c.QueryParam("role"); role != "" {
		if !slices.Contains(allRoles, role) {
			return invalid("unknown role")
		}
		args = append(args, role)
		q += " WHERE role = $1::user_role"
	}
	args = append(args, limit, offset)
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := h.DB.Query(c.Request().Context(), q, args...)
	if err != nil {
		return internalErr(err)
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := scanUser(rows, &u); err != nil {
			return internalErr(err)
		}
		out = append(out, u)
	}
	return c.JSON(http.StatusOK, out)
}

// PATCH /api/admin/users/:id  (super_admin) - change role and/or deactivate
func (h *Handler) UpdateUser(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	if id == authmw.UserID(c) {
		return echo.NewHTTPError(http.StatusBadRequest, "you cannot change your own role or status")
	}
	var r struct {
		Role     *string `json:"role"`
		IsActive *bool   `json:"is_active"`
	}
	if err := c.Bind(&r); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if r.Role != nil && !slices.Contains(allRoles, *r.Role) {
		return invalid("unknown role")
	}
	var u User
	err = scanUser(h.DB.QueryRow(c.Request().Context(),
		`UPDATE users SET role = COALESCE($2::user_role, role), is_active = COALESCE($3, is_active), updated_at = now()
		 WHERE id=$1 RETURNING `+userCols, id, r.Role, r.IsActive), &u)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("user")
	}
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, u)
}

// GET /api/admin/users/:id  (super_admin)
func (h *Handler) GetUser(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	var u User
	err = scanUser(h.DB.QueryRow(c.Request().Context(), `SELECT `+userCols+` FROM users WHERE id=$1`, id), &u)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("user")
	}
	if err != nil {
		return internalErr(err)
	}
	return c.JSON(http.StatusOK, u)
}

// DELETE /api/admin/users/:id  (super_admin) - fails if the user owns posts/enrollments; deactivate instead
func (h *Handler) DeleteUser(c echo.Context) error {
	id, err := idParam(c, "id")
	if err != nil {
		return err
	}
	if id == authmw.UserID(c) {
		return echo.NewHTTPError(http.StatusBadRequest, "you cannot delete your own account")
	}
	tag, err := h.DB.Exec(c.Request().Context(), `DELETE FROM users WHERE id=$1`, id)
	if isForeignKeyViolation(err) {
		return echo.NewHTTPError(http.StatusConflict, "this user has posts or enrollments; deactivate the account instead")
	}
	if err != nil {
		return internalErr(err)
	}
	if tag.RowsAffected() == 0 {
		return notFound("user")
	}
	return c.NoContent(http.StatusNoContent)
}
