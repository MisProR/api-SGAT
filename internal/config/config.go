// Package config carga la configuración de la API desde variables de entorno (.env).
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Env  string
	Port string

	DatabaseURL    string
	DBMaxConns     int32
	DBMinConns     int32
	DBQueryTimeout time.Duration

	JWTSecret       string
	JWTIssuer       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	CookieSecure bool
	CookieDomain string

	CORSOrigins []string

	// UploadsDir es la carpeta donde se guardan los archivos subidos (actas firmadas escaneadas)
	UploadsDir string

	LoginRateLimit  int // intentos por minuto por IP
	MaxLoginFails   int
	LockoutDuration time.Duration
}

func (c Config) IsProduction() bool { return c.Env == "production" }

// Load lee el archivo .env (si existe) y valida las variables obligatorias.
func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		Env:             getEnv("APP_ENV", "development"),
		Port:            getEnv("APP_PORT", "8080"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		DBMaxConns:      int32(getInt("DB_MAX_CONNS", 20)),
		DBMinConns:      int32(getInt("DB_MIN_CONNS", 2)),
		DBQueryTimeout:  getDuration("DB_QUERY_TIMEOUT", 15*time.Second),
		JWTSecret:       os.Getenv("JWT_SECRET"),
		JWTIssuer:       getEnv("JWT_ISSUER", "sgat-api"),
		AccessTokenTTL:  getDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL: getDuration("REFRESH_TOKEN_TTL", 7*24*time.Hour),
		CookieSecure:    getBool("COOKIE_SECURE", false),
		CookieDomain:    os.Getenv("COOKIE_DOMAIN"),
		CORSOrigins:     splitList(getEnv("CORS_ORIGINS", "http://localhost:4200")),
		UploadsDir:      getEnv("UPLOADS_DIR", "uploads"),
		LoginRateLimit:  getInt("LOGIN_RATE_LIMIT", 10),
		MaxLoginFails:   getInt("MAX_LOGIN_FAILS", 5),
		LockoutDuration: getDuration("LOCKOUT_DURATION", 15*time.Minute),
	}

	var errs []error
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL es obligatorio"))
	}
	if len(cfg.JWTSecret) < 32 {
		errs = append(errs, errors.New("JWT_SECRET debe tener al menos 32 caracteres"))
	}
	if cfg.IsProduction() && !cfg.CookieSecure {
		errs = append(errs, errors.New("COOKIE_SECURE debe ser true en producción"))
	}
	if err := errors.Join(errs...); err != nil {
		return cfg, fmt.Errorf("configuración inválida: %w", err)
	}
	return cfg, nil
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func getBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func getDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
