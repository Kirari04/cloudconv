package config

import (
	"crypto/rand"
	"encoding/base64"
	"log"
	"os"
	"strconv"
)

type Config struct {
	Addr         string
	DBPath       string
	UploadDir    string
	ConvertedDir string
	SetupToken   string
	CookieSecure bool
	TrustProxy   bool
}

func Load() Config {
	cfg := Config{
		Addr:         env("CLOUDCONV_ADDR", ":3000"),
		DBPath:       env("CLOUDCONV_DB_PATH", "data/cloudconv.db"),
		UploadDir:    env("CLOUDCONV_UPLOAD_DIR", "uploads"),
		ConvertedDir: env("CLOUDCONV_CONVERTED_DIR", "converted"),
		SetupToken:   os.Getenv("CLOUDCONV_SETUP_TOKEN"),
		CookieSecure: envBool("CLOUDCONV_COOKIE_SECURE", false),
		TrustProxy:   envBool("CLOUDCONV_TRUST_PROXY", false),
	}
	if cfg.SetupToken == "" {
		cfg.SetupToken = randomToken()
		log.Printf("CLOUDCONV_SETUP_TOKEN is not set. First-run setup token: %s", cfg.SetupToken)
	}
	return cfg
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}
