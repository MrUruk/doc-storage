package config

import (
	"os"

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
