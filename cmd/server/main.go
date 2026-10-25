package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"

	"schoolapp/internal/authmw"
	"schoolapp/internal/config"
	"schoolapp/internal/db"
	"schoolapp/internal/handlers"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if err := db.SeedSuperAdmin(ctx, pool, cfg.SeedEmail, cfg.SeedPassword); err != nil {
		log.Fatalf("seed: %v", err)
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o750); err != nil {
		log.Fatalf("upload dir: %v", err)
	}

	h := &handlers.Handler{DB: pool, Cfg: cfg}

	e := echo.New()
	e.HideBanner = true
	// Behind Caddy/nginx on a private network, trust X-Real-IP; direct clients fall back to the socket IP.
	e.IPExtractor = echo.ExtractIPFromRealIPHeader()

	e.Use(echomw.Recover(), echomw.RequestID(), echomw.Logger(), echomw.Secure())
	e.Use(echomw.BodyLimit(fmt.Sprintf("%dM", cfg.MaxUploadMB+1)))
	if len(cfg.CORSOrigins) > 0 {
		e.Use(echomw.CORSWithConfig(echomw.CORSConfig{
			AllowOrigins: cfg.CORSOrigins,
			AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowHeaders: []string{"Authorization", "Content-Type"},
		}))
	}

	// ---- public pages
	e.GET("/healthz", func(c echo.Context) error {
		if err := pool.Ping(c.Request().Context()); err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "db down")
		}
		return c.String(http.StatusOK, "ok")
	})

	// ---- public API
	api := e.Group("/api")
	limiter := echomw.RateLimiterWithConfig(echomw.RateLimiterConfig{
		Store: echomw.NewRateLimiterMemoryStoreWithConfig(echomw.RateLimiterMemoryStoreConfig{
			Rate: 0.5, Burst: 10, ExpiresIn: 5 * time.Minute,
		}),
	})
	api.POST("/auth/register", h.Register, limiter)
	api.POST("/auth/login", h.Login, limiter)
	api.GET("/news", h.ListNewsAPI)
	api.GET("/news/:slug", h.GetNewsAPI)
	api.GET("/academic-years", h.ListAcademicYears)
	api.GET("/profile", h.GetProfile)

	// ---- authenticated API
	authed := api.Group("", authmw.JWT(cfg.JWTSecret, h.LookupUser))
	authed.GET("/me", h.Me)
	authed.POST("/enrollments", h.CreateEnrollment)
	authed.GET("/enrollments", h.MyEnrollments)
	authed.GET("/enrollments/:id", h.GetEnrollment)
	authed.PUT("/enrollments/:id", h.UpdateEnrollment)
	authed.POST("/enrollments/:id/documents", h.UploadDocument)
	authed.DELETE("/enrollments/:id/documents/:docId", h.DeleteDocument)
	authed.GET("/enrollments/:id/documents/:docId", h.DownloadDocument)

	// ---- admin API (role checks are per route)
	super := authmw.Require(authmw.RoleSuperAdmin)
	newsAdm := authmw.Require(authmw.RoleNewsAdmin, authmw.RoleSuperAdmin)
	enrAdm := authmw.Require(authmw.RoleEnrollmentAdmin, authmw.RoleSuperAdmin)

	authed.POST("/admin/users", h.CreateAdmin, super)
	authed.GET("/admin/users", h.ListUsers, super)
	authed.GET("/admin/users/:id", h.GetUser, super)
	authed.PATCH("/admin/users/:id", h.UpdateUser, super)
	authed.DELETE("/admin/users/:id", h.DeleteUser, super)

	authed.PUT("/admin/profile", h.UpdateProfile, super)

	authed.GET("/admin/news", h.ListNewsAdmin, newsAdm)
	authed.GET("/admin/news/:id", h.GetPostAdmin, newsAdm)
	authed.POST("/admin/news", h.CreatePost, newsAdm)
	authed.PUT("/admin/news/:id", h.UpdatePost, newsAdm)
	authed.DELETE("/admin/news/:id", h.DeletePost, newsAdm)

	authed.POST("/admin/academic-years", h.CreateAcademicYear, enrAdm)
	authed.PUT("/admin/academic-years/:id", h.UpdateAcademicYear, enrAdm)
	authed.GET("/admin/enrollments", h.ListEnrollmentsAdmin, enrAdm)
	authed.PATCH("/admin/enrollments/:id/status", h.UpdateEnrollmentStatus, enrAdm)
	authed.DELETE("/admin/enrollments/:id", h.DeleteEnrollment, enrAdm)

	go func() {
		if err := e.Start(":" + cfg.Port); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
