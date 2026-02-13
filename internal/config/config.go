package config

import (
	"fmt"
	"os"
)

// Config holds runtime configuration loaded from environment variables.
type Config struct {
	Environment string
	LogLevel    string
	PostgresURL string
	MinIOURL    string
	MinIOUser   string
	MinIOPass   string
	S3Bucket    string
}

func LoadFromEnv() (Config, error) {
	cfg := Config{
		Environment: getOrDefault("APP_ENV", "development"),
		LogLevel:    getOrDefault("LOG_LEVEL", "info"),
		PostgresURL: getOrDefault("POSTGRES_URL", "postgres://postgres:postgres@localhost:5432/diffmind?sslmode=disable"),
		MinIOURL:    getOrDefault("MINIO_URL", "http://localhost:9000"),
		MinIOUser:   getOrDefault("MINIO_ROOT_USER", "minioadmin"),
		MinIOPass:   getOrDefault("MINIO_ROOT_PASSWORD", "minioadmin"),
		S3Bucket:    getOrDefault("S3_BUCKET", "diffmind"),
	}

	if cfg.S3Bucket == "" {
		return Config{}, fmt.Errorf("S3_BUCKET cannot be empty")
	}

	return cfg, nil
}

func getOrDefault(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
