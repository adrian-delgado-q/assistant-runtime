package config

import (
	"fmt"
	"os"
)

type Config struct {
	DBPath string

	MetaVerifyToken   string
	MetaAppSecret     string
	MetaAccessToken   string
	MetaPhoneNumberID string

	DeepSeekAPIKey string

	SlackWebhookURL    string
	SlackSigningSecret string
}

// Load reads all required environment variables. Fails fast if any are missing.
func Load() (*Config, error) {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/db.sqlite" // default: Docker volume path
	}

	c := &Config{
		DBPath:             dbPath,
		MetaVerifyToken:    os.Getenv("META_VERIFY_TOKEN"),
		MetaAppSecret:      os.Getenv("META_APP_SECRET"),
		MetaAccessToken:    os.Getenv("META_ACCESS_TOKEN"),
		MetaPhoneNumberID:  os.Getenv("META_PHONE_NUMBER_ID"),
		DeepSeekAPIKey:     os.Getenv("DEEPSEEK_API_KEY"),
		SlackWebhookURL:    os.Getenv("SLACK_WEBHOOK_URL"),
		SlackSigningSecret: os.Getenv("SLACK_SIGNING_SECRET"),
	}

	required := map[string]string{
		"META_VERIFY_TOKEN":    c.MetaVerifyToken,
		"META_APP_SECRET":      c.MetaAppSecret,
		"META_ACCESS_TOKEN":    c.MetaAccessToken,
		"META_PHONE_NUMBER_ID": c.MetaPhoneNumberID,
		"DEEPSEEK_API_KEY":     c.DeepSeekAPIKey,
		"SLACK_WEBHOOK_URL":    c.SlackWebhookURL,
		"SLACK_SIGNING_SECRET": c.SlackSigningSecret,
	}

	for key, val := range required {
		if val == "" {
			return nil, fmt.Errorf("missing required environment variable: %s", key)
		}
	}

	return c, nil
}
