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

func TestEnvPull(t *testing.T) {
	dir, cleanup := setupTestStore(t)
	defer cleanup()

	// Push secrets first
	envPath := filepath.Join(dir, "source.env")
	os.WriteFile(envPath, []byte("DB_HOST=localhost\nAPI_KEY=sk-123\n"), 0644)
	envPush(envPath, "test-project", "merge")

	// Pull to new file
	outPath := filepath.Join(dir, ".env")
	err := envPull(outPath, "test-project", true)
	if err != nil {
		t.Fatalf("pull failed: %v", err)
	}

	vars, err := parseEnvFile(outPath)
	if err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if vars["DB_HOST"] != "localhost" {
		t.Errorf("expected 'localhost', got %q", vars["DB_HOST"])
	}
	if vars["API_KEY"] != "sk-123" {
		t.Errorf("expected 'sk-123', got %q", vars["API_KEY"])
	}
}

func TestEnvPullNoForceExistingFile(t *testing.T) {
	dir, cleanup := setupTestStore(t)
	defer cleanup()

	outPath := filepath.Join(dir, ".env")
	os.WriteFile(outPath, []byte("EXISTING=true\n"), 0644)

	err := envPull(outPath, "test-project", false)
	if err == nil {
		t.Fatal("expected error when file exists without --force")
	}
}

func TestEnvListProjects(t *testing.T) {
	_, cleanup := setupTestStore(t)
	defer cleanup()

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("KEY1=val1\nKEY2=val2\n"), 0644)
	envPush(envPath, "alpha", "merge")

	os.WriteFile(envPath, []byte("KEY3=val3\n"), 0644)
	envPush(envPath, "beta", "merge")

	projects, err := envListProjects()
	if err != nil {
		t.Fatalf("list projects failed: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}

	found := make(map[string]int)
	for _, p := range projects {
		found[p.name] = p.count
	}
	if found["alpha"] != 2 {
		t.Errorf("expected alpha=2, got %d", found["alpha"])
	}
	if found["beta"] != 1 {
		t.Errorf("expected beta=1, got %d", found["beta"])
	}
}

func TestEnvListKeys(t *testing.T) {
	_, cleanup := setupTestStore(t)
	defer cleanup()

	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	os.WriteFile(envPath, []byte("DB_HOST=localhost\nAPI_KEY=sk-abcdef123\n"), 0644)
	envPush(envPath, "myproject", "merge")

	entries, err := envListKeys("myproject", false)
	if err != nil {
		t.Fatalf("list keys failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	for _, e := range entries {
		if e.key == "API_KEY" && e.maskedValue != "sk-a***" {
			t.Errorf("expected masked 'sk-a***', got %q", e.maskedValue)
		}
		if e.key == "DB_HOST" && e.maskedValue != "loca***" {
			t.Errorf("expected masked 'loca***', got %q", e.maskedValue)
		}
	}
}

func TestMaskValue(t *testing.T) {
	tests := []struct {
		val, expected string
		reveal        bool
	}{
		{"abcdefgh", "abcd***", false},
		{"abc", "******", false},
		{"abc123", "******", false},   // exactly 6 chars
		{"abcdefg", "abcd***", false}, // exactly 7 chars
		{"abcdefgh", "abcdefgh", true},
		{"abc", "abc", true},
	}

	for _, tt := range tests {
		got := maskValue(tt.val, tt.reveal)
		if got != tt.expected {
			t.Errorf("maskValue(%q, %v) = %q, want %q", tt.val, tt.reveal, got, tt.expected)
		}
	}
}
