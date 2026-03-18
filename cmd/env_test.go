package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/iminaii/iscrt/backend"
	"github.com/iminaii/iscrt/store"
	"github.com/spf13/viper"
)

// setupTestStore creates a temporary store and configures viper for testing.
func setupTestStore(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "test.iscrt")
	password := "test-password"

	viper.Set(configKeyPassword, password)
	viper.Set(configKeyStorage, "local")
	viper.Set("local-path", storePath)

	// Initialize empty store
	b, err := backend.Backends["local"].New(viper.AllSettings())
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	s := store.NewStore()
	data, err := store.WriteStore([]byte(password), s)
	if err != nil {
		t.Fatalf("failed to write store: %v", err)
	}
	if err := b.Save(data); err != nil {
		t.Fatalf("failed to save store: %v", err)
	}

	return dir, func() {
		viper.Reset()
	}
}

// loadTestStore reads and decrypts the test store.
func loadTestStore(t *testing.T) store.Store {
	t.Helper()
	password := viper.GetString(configKeyPassword)
	b, err := backend.Backends["local"].New(viper.AllSettings())
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	data, err := b.Load()
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}
	s, err := store.ReadStore([]byte(password), data)
	if err != nil {
		t.Fatalf("failed to decrypt: %v", err)
	}
	return s
}

func TestEnvPush(t *testing.T) {
	dir, cleanup := setupTestStore(t)
	defer cleanup()

	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("DB_HOST=localhost\nAPI_KEY=sk-123\n"), 0644)

	err := envPush(envPath, "test-project", "merge")
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	s := loadTestStore(t)
	val, err := s.Get("test-project/DB_HOST")
	if err != nil {
		t.Fatalf("missing key: %v", err)
	}
	if string(val) != "localhost" {
		t.Errorf("expected 'localhost', got %q", string(val))
	}

	val, err = s.Get("test-project/API_KEY")
	if err != nil {
		t.Fatalf("missing key: %v", err)
	}
	if string(val) != "sk-123" {
		t.Errorf("expected 'sk-123', got %q", string(val))
	}
}

func TestEnvPushReplace(t *testing.T) {
	dir, cleanup := setupTestStore(t)
	defer cleanup()

	// Pre-populate store
	password := viper.GetString(configKeyPassword)
	b, _ := backend.Backends["local"].New(viper.AllSettings())
	data, _ := b.Load()
	s, _ := store.ReadStore([]byte(password), data)
	s.Set("test-project/OLD_KEY", []byte("old-value"))
	data, _ = store.WriteStore([]byte(password), s)
	b.Save(data)

	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("NEW_KEY=new-value\n"), 0644)

	err := envPush(envPath, "test-project", "replace")
	if err != nil {
		t.Fatalf("push replace failed: %v", err)
	}

	s = loadTestStore(t)
	if !s.Has("test-project/NEW_KEY") {
		t.Error("NEW_KEY should exist")
	}
	if s.Has("test-project/OLD_KEY") {
		t.Error("OLD_KEY should be removed in replace mode")
	}
}

func TestEnvPushMergePreservesExisting(t *testing.T) {
	dir, cleanup := setupTestStore(t)
	defer cleanup()

	password := viper.GetString(configKeyPassword)
	b, _ := backend.Backends["local"].New(viper.AllSettings())
	data, _ := b.Load()
	s, _ := store.ReadStore([]byte(password), data)
	s.Set("test-project/EXISTING_KEY", []byte("existing-value"))
	data, _ = store.WriteStore([]byte(password), s)
	b.Save(data)

	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("NEW_KEY=new-value\n"), 0644)

	err := envPush(envPath, "test-project", "merge")
	if err != nil {
		t.Fatalf("push merge failed: %v", err)
	}

	s = loadTestStore(t)
	if !s.Has("test-project/NEW_KEY") {
		t.Error("NEW_KEY should exist")
	}
	if !s.Has("test-project/EXISTING_KEY") {
		t.Error("EXISTING_KEY should be preserved in merge mode")
	}
}
