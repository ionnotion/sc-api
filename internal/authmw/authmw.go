package authmw

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"schoolapp/internal/auth"
)

const (
	RoleUser            = "user"
	RoleNewsAdmin       = "news_admin"
	RoleEnrollmentAdmin = "enrollment_admin"
	RoleSuperAdmin      = "super_admin"
)

// Lookup returns the user's CURRENT role and active flag, so role changes and
// deactivation take effect immediately instead of waiting for the token to expire.
type Lookup func(ctx context.Context, userID uuid.UUID) (role string, active bool, err error)

func JWT(secret string, lookup Lookup) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			tok, ok := strings.CutPrefix(c.Request().Header.Get("Authorization"), "Bearer ")
			if !ok {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing bearer token")
			}
			sub, err := auth.ParseToken(tok, secret)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid or expired token")
			}
			id, err := uuid.Parse(sub)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
			}
			role, active, err := lookup(c.Request().Context(), id)
			if err != nil || !active {
				return echo.NewHTTPError(http.StatusUnauthorized, "account not found or disabled")
			}
			c.Set("uid", id)
			c.Set("role", role)
			return next(c)
		}
	}
}

// Require allows only the listed roles.
func Require(roles ...string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if !slices.Contains(roles, Role(c)) {
				return echo.NewHTTPError(http.StatusForbidden, "insufficient permissions")
			}
			return next(c)
		}
	}
}

func UserID(c echo.Context) uuid.UUID { id, _ := c.Get("uid").(uuid.UUID); return id }
func Role(c echo.Context) string      { r, _ := c.Get("role").(string); return r }
