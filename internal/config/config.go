package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Homeserver      string
	UserID          string
	Password        string
	AccessToken     string // optional; use instead of Password for token-based auth
	DeviceID        string // optional; for token auth, auto-fetched via /whoami if not set
	RecoveryKey     string // optional; Matrix recovery key used to self-verify when cross-signing exists
	StorePath       string
	PickleKey       []byte
	WhisperModel    string
	WhisperLanguage string
	WhisperModelDir string
	WhisperThreads  int
	PythonBin       string
}

func LoadFromEnv() (*Config, error) {
	cfg := &Config{
		Homeserver:      os.Getenv("MATRIX_HOMESERVER"),
		UserID:          os.Getenv("MATRIX_USER_ID"),
		Password:        os.Getenv("MATRIX_PASSWORD"),
		AccessToken:     os.Getenv("MATRIX_ACCESS_TOKEN"),
		DeviceID:        os.Getenv("MATRIX_DEVICE_ID"),
		RecoveryKey:     os.Getenv("MATRIX_RECOVERY_KEY"),
		StorePath:       getenv("STORE_PATH", "/app/store"),
		WhisperModel:    getenv("WHISPER_MODEL", "large-v3"),
		WhisperLanguage: getenv("WHISPER_LANGUAGE", "es"),
		WhisperModelDir: getenv("WHISPER_MODEL_DIR", "/app/models"),
		PythonBin:       getenv("PYTHON_BIN", "python"),
		PickleKey:       []byte(os.Getenv("PICKLE_KEY")),
	}

	if cfg.Homeserver == "" || cfg.UserID == "" {
		return nil, errors.New("MATRIX_HOMESERVER and MATRIX_USER_ID are required")
	}
	if cfg.Password == "" && cfg.AccessToken == "" {
		return nil, errors.New("either MATRIX_PASSWORD or MATRIX_ACCESS_TOKEN is required")
	}
	if len(cfg.PickleKey) == 0 {
		return nil, errors.New("PICKLE_KEY is required for E2EE (use a long random secret string)")
	}

	threads, err := strconv.Atoi(getenv("WHISPER_CPU_THREADS", "0"))
	if err != nil {
		return nil, fmt.Errorf("invalid WHISPER_CPU_THREADS: %w", err)
	}
	cfg.WhisperThreads = threads

	if err := os.MkdirAll(cfg.StorePath, 0o755); err != nil {
		return nil, fmt.Errorf("create store path: %w", err)
	}
	if err := os.MkdirAll(cfg.WhisperModelDir, 0o755); err != nil {
		return nil, fmt.Errorf("create model path: %w", err)
	}

	return cfg, nil
}

// CryptoDB returns the path to the SQLite database used for E2EE key storage.
func (c *Config) CryptoDB() string {
	return filepath.Join(c.StorePath, "crypto.db")
}

func getenv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}
