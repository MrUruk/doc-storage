package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// InitEnv loads environment variables from a single .env file, selected by
// APP_ENV (falling back to GO_ENV):
//
//   - unset                -> .env             (default; e.g. the service in a container)
//   - APP_ENV=development   -> .env.development (a local instance)
//
// This lets one machine run the app from different .env files: leave APP_ENV
// unset to use .env, or set APP_ENV=development to use .env.development. Real
// process environment variables always take precedence over the file.
func InitEnv() {
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = os.Getenv("GO_ENV")
	}

	file := ".env"
	if env != "" {
		file = ".env." + env
	}

	_ = godotenv.Load(file)
}

// IsDev reports whether the app runs in development mode.
func IsDev() bool {
	return os.Getenv("GIN_MODE") == "debug"
}

// GetEnvPanic returns the value for key, panicking if it is unset/empty. Use it
// for configuration the service cannot run without.
func GetEnvPanic(key string) string {
	value := os.Getenv(key)
	if value == "" {
		panic(fmt.Sprintf("Env %s not provided", key))
	}
	return value
}

// GetEnv returns the value for key, or fallback when it is unset/empty.
func GetEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// GetEnvInt returns the integer value for key, or fallback when unset/invalid.
func GetEnvInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return fallback
}
