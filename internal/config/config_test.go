package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromEnvRequiresMatrixCreds(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER", "")
	t.Setenv("MATRIX_USER_ID", "")
	t.Setenv("MATRIX_PASSWORD", "")
	t.Setenv("PICKLE_KEY", "")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error when required env vars are missing")
	}
}

func TestLoadFromEnvRequiresPickleKey(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MATRIX_HOMESERVER", "https://matrix.example.com")
	t.Setenv("MATRIX_USER_ID", "@bot:example.com")
	t.Setenv("MATRIX_PASSWORD", "secret")
	t.Setenv("STORE_PATH", filepath.Join(root, "store"))
	t.Setenv("WHISPER_MODEL_DIR", filepath.Join(root, "models"))
	t.Setenv("PICKLE_KEY", "")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error when PICKLE_KEY is missing")
	}
}

func TestLoadFromEnvParsesThreadsAndPaths(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "store")
	models := filepath.Join(root, "models")

	t.Setenv("MATRIX_HOMESERVER", "https://matrix.example.com")
	t.Setenv("MATRIX_USER_ID", "@bot:example.com")
	t.Setenv("MATRIX_PASSWORD", "secret")
	t.Setenv("STORE_PATH", store)
	t.Setenv("WHISPER_MODEL_DIR", models)
	t.Setenv("WHISPER_CPU_THREADS", "4")
	t.Setenv("PICKLE_KEY", "a-very-secret-pickle-key-for-testing")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if cfg.WhisperThreads != 4 {
		t.Fatalf("expected 4 threads, got %d", cfg.WhisperThreads)
	}
	if _, err := os.Stat(store); err != nil {
		t.Fatalf("store path not created: %v", err)
	}
	if _, err := os.Stat(models); err != nil {
		t.Fatalf("model path not created: %v", err)
	}
	if cfg.CryptoDB() != filepath.Join(store, "crypto.db") {
		t.Fatalf("unexpected CryptoDB path: %s", cfg.CryptoDB())
	}
	if string(cfg.PickleKey) != "a-very-secret-pickle-key-for-testing" {
		t.Fatalf("unexpected PickleKey: %s", cfg.PickleKey)
	}
}
