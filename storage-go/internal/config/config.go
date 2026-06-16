package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// InitEnv loads environment variables from a single .env file, selected by
// GO_ENV:
//
//   - unset                -> .env             (default; e.g. the service in a container)
//   - GO_ENV=development    -> .env.development (a local instance)
//
// This lets one machine run the app from different .env files: leave GO_ENV
// unset to use .env, or set GO_ENV=development to use .env.development. Real
// process environment variables always take precedence over the file.
func InitEnv() {
	env := os.Getenv("GO_ENV")

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

// MustEnv returns the value for key, panicking if it is unset/empty. It is the
// single fail-fast accessor for required configuration.
func MustEnv(key string) string {
	value := os.Getenv(key)
	if value == "" {
		panic(fmt.Sprintf("Env %s not provided", key))
	}
	return value
}
