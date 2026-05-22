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
	StorePath       string
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
		StorePath:       getenv("STORE_PATH", "/app/store"),
		WhisperModel:    getenv("WHISPER_MODEL", "large-v3"),
		WhisperLanguage: getenv("WHISPER_LANGUAGE", "es"),
		WhisperModelDir: getenv("WHISPER_MODEL_DIR", "/app/models"),
		PythonBin:       getenv("PYTHON_BIN", "python"),
	}

	if cfg.Homeserver == "" || cfg.UserID == "" || cfg.Password == "" {
		return nil, errors.New("MATRIX_HOMESERVER, MATRIX_USER_ID, and MATRIX_PASSWORD are required")
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

func (c *Config) SessionFile() string {
	return filepath.Join(c.StorePath, "session.json")
}

func getenv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}
