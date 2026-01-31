package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Email         string
	Password      string
	BackupDir     string
	ImapServer    string
	ImapPort      int
	FoldersOnly   map[string]bool
	MaxWorkers    int
	DryRun        bool
	TLSSkipVerify bool
	LogLevel      string

	ClientID        string
	ClientSecret    string
	OAuth2TokenFile string
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		return strings.ToLower(v) == "true"
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

func LoadConfig() Config {
	folders := map[string]bool{}
	if v := os.Getenv("FOLDERS_ONLY"); v != "" {
		for _, f := range strings.Split(v, ",") {
			folders[strings.TrimSpace(f)] = true
		}
	}

	home, _ := os.UserHomeDir()
	defaultTokenFile := filepath.Join(home, ".config", "archive_gmail", "token.json")

	return Config{
		Email:           os.Getenv("GMAIL_EMAIL"),
		Password:        os.Getenv("GMAIL_PASSWORD"),
		BackupDir:       getenv("BACKUP_DIR", "./backups"),
		ImapServer:      getenv("IMAP_SERVER", "imap.gmail.com"),
		ImapPort:        getenvInt("IMAP_PORT", 993),
		FoldersOnly:     folders,
		MaxWorkers:      getenvInt("MAX_WORKERS", 1),
		DryRun:          getenvBool("DRY_RUN", false),
		TLSSkipVerify:   getenvBool("TLS_SKIP_VERIFY", false),
		LogLevel:        getenv("LOG_LEVEL", "INFO"),
		ClientID:        getenv("GMAIL_CLIENT_ID", ""),
		ClientSecret:    getenv("GMAIL_CLIENT_SECRET", ""),
		OAuth2TokenFile: getenv("OAUTH2_TOKEN_FILE", defaultTokenFile),
	}
}
