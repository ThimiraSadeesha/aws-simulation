// Package config loads backend configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr              string
	DatabasePath      string
	ObjectStorageDir  string
	BackupDir         string
	LabsDir           string
	EngineURL         string
	EngineToken       string
	AllowRegistration bool
	SecureCookies     bool
	SessionTTL        time.Duration
	RateLimitRPS      float64
	RateLimitBurst    int
	LoginPerMinute    int
	MaxObjectSize     int64
	LogLevel          string
	AllowedOrigins    []string
	MetricsInterval   time.Duration
	LifecycleInterval time.Duration
	ASGInterval       time.Duration
	AlarmInterval     time.Duration
	MetricsRetention  time.Duration
	Version           string
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v := strings.ToLower(env(k, ""))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(env(k, "")); err == nil {
		return d
	}
	return def
}

// Load reads configuration; it returns an error for unsafe or invalid settings.
func Load() (*Config, error) {
	c := &Config{
		Addr:              env("BACKEND_ADDR", ":8080"),
		DatabasePath:      env("DATABASE_PATH", "./data/cloudsim.db"),
		ObjectStorageDir:  env("OBJECT_STORAGE_DIR", "./data/objects"),
		BackupDir:         env("BACKUP_DIR", "./data/backups"),
		LabsDir:           env("LABS_DIR", "../training-labs"),
		EngineURL:         strings.TrimRight(env("ENGINE_URL", "http://localhost:7070"), "/"),
		EngineToken:       env("ENGINE_TOKEN", ""),
		AllowRegistration: envBool("ALLOW_REGISTRATION", true),
		SecureCookies:     envBool("SECURE_COOKIES", false),
		SessionTTL:        envDur("SESSION_TTL", 24*time.Hour),
		LogLevel:          env("LOG_LEVEL", "info"),
		MetricsInterval:   envDur("METRICS_INTERVAL", 10*time.Second),
		LifecycleInterval: envDur("LIFECYCLE_INTERVAL", 60*time.Second),
		ASGInterval:       envDur("ASG_INTERVAL", 15*time.Second),
		AlarmInterval:     envDur("ALARM_INTERVAL", 15*time.Second),
		MetricsRetention:  envDur("METRICS_RETENTION", 24*time.Hour),
		Version:           env("CLOUDSIM_VERSION", "0.1.0"),
	}
	var err error
	if c.RateLimitRPS, err = strconv.ParseFloat(env("RATE_LIMIT_RPS", "50"), 64); err != nil {
		return nil, fmt.Errorf("RATE_LIMIT_RPS: %w", err)
	}
	if c.RateLimitBurst, err = strconv.Atoi(env("RATE_LIMIT_BURST", "100")); err != nil {
		return nil, fmt.Errorf("RATE_LIMIT_BURST: %w", err)
	}
	if c.LoginPerMinute, err = strconv.Atoi(env("LOGIN_RATE_PER_MINUTE", "10")); err != nil {
		return nil, fmt.Errorf("LOGIN_RATE_PER_MINUTE: %w", err)
	}
	if c.MaxObjectSize, err = strconv.ParseInt(env("MAX_OBJECT_SIZE", "104857600"), 10, 64); err != nil {
		return nil, fmt.Errorf("MAX_OBJECT_SIZE: %w", err)
	}
	if o := env("ALLOWED_ORIGINS", ""); o != "" {
		for _, s := range strings.Split(o, ",") {
			c.AllowedOrigins = append(c.AllowedOrigins, strings.TrimSpace(s))
		}
	}
	if c.EngineToken == "" {
		return nil, fmt.Errorf("ENGINE_TOKEN must be set (shared secret between backend and engine)")
	}
	return c, nil
}
