package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

type Site struct {
	Name, Email, Phone, Address string
}

type Config struct {
	Port         string
	DatabaseURL  string
	JWTSecret    string
	UploadDir    string
	MaxUploadMB  int64
	CORSOrigins  []string
	SeedEmail    string
	SeedPassword string
	Site         Site
}

func Load() Config {
	c := Config{
		Port:         env("PORT", "8080"),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		JWTSecret:    os.Getenv("JWT_SECRET"),
		UploadDir:    env("UPLOAD_DIR", "./uploads"),
		SeedEmail:    os.Getenv("SEED_ADMIN_EMAIL"),
		SeedPassword: os.Getenv("SEED_ADMIN_PASSWORD"),
		Site: Site{
			Name:    env("SITE_NAME", "Our School"),
			Email:   os.Getenv("SITE_EMAIL"),
			Phone:   os.Getenv("SITE_PHONE"),
			Address: os.Getenv("SITE_ADDRESS"),
		},
	}
	mb, err := strconv.ParseInt(env("MAX_UPLOAD_MB", "5"), 10, 64)
	if err != nil || mb < 1 {
		mb = 5
	}
	c.MaxUploadMB = mb
	for _, o := range strings.Split(os.Getenv("CORS_ORIGINS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, o)
		}
	}
	if c.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	if len(c.JWTSecret) < 32 {
		log.Fatal("JWT_SECRET is required and must be at least 32 characters")
	}
	return c
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
