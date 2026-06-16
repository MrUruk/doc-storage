// Package config loads runtime configuration from the environment, mirroring
// the Python params.Params. A local .env file is loaded if present.
package config

import (
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Host string
	Port int

	PandocServiceURL string

	QdrantHost       string
	QdrantPort       int
	QdrantCollection string

	ModelName           string
	ModelURL            string
	ModelPath           string
	EmbeddingServiceURL string
	MaxLength           int

	DatabaseURL string
	S3URL       string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string

	LogLevel string
}

// Load reads configuration from the environment with the same defaults as the
// Python implementation.
func Load() *Config {
	_ = godotenv.Load() // .env is optional

	return &Config{
		Host:             get("HOST", "localhost"),
		Port:             getInt("PORT", 8080),
		PandocServiceURL: get("PANDOC_SERVICE_URL", "http://localhost:9797"),

		QdrantHost:       get("QDRANT_HOST", "localhost"),
		QdrantPort:       getInt("QDRANT_PORT", 6333),
		QdrantCollection: get("QDRANT_COLLECTION", "qwen3_html_agentic_collection"),

		ModelName:           get("MODEL_NAME", "google/gemma-4-26B-A4B-it"),
		ModelURL:            get("MODEL_URL", "http://localhost:9003"),
		ModelPath:           get("MODEL_PATH", "./models"),
		EmbeddingServiceURL: get("EMBEDDING_SERVICE_URL", "http://localhost:9003/v1/embeddings"),
		MaxLength:           getInt("MAX_LENGTH", 1000),

		DatabaseURL: get("DATABASE_URL", "postgres://postgres:postgres@localhost:9999/agents?sslmode=disable"),
		S3URL:       get("S3_URL", "localhost:9000"),
		S3Bucket:    get("S3_BUCKET", "docs"),
		S3AccessKey: get("RUSTFS_ACCESS_KEY", "rustfsadmin"),
		S3SecretKey: get("RUSTFS_SECRET_KEY", "rustfsadmin"),

		LogLevel: get("LOG_LEVEL", "info"),
	}
}

func get(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
