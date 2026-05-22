package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("project: my-project\nregion: us-east1\noutput: json\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Project != "my-project" {
		t.Errorf("expected project 'my-project', got %q", cfg.Project)
	}
	if cfg.Region != "us-east1" {
		t.Errorf("expected region 'us-east1', got %q", cfg.Region)
	}
	if cfg.Output != "json" {
		t.Errorf("expected output 'json', got %q", cfg.Output)
	}
}

func TestLoad_PartialConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("project: only-project\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Project != "only-project" {
		t.Errorf("expected project 'only-project', got %q", cfg.Project)
	}
	if cfg.Region != "" {
		t.Errorf("expected empty region, got %q", cfg.Region)
	}
	if cfg.Output != "" {
		t.Errorf("expected empty output, got %q", cfg.Output)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if cfg.Project != "" || cfg.Region != "" {
		t.Error("expected empty config for missing file")
	}
}

func TestLoad_EmptyPath(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("{{invalid yaml:::"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project != "" || cfg.Region != "" {
		t.Error("expected empty config for empty file")
	}
}

func TestLoad_ExtraFieldsIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "project: p1\nregion: r1\nunknown_field: value\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Project != "p1" {
		t.Errorf("expected project 'p1', got %q", cfg.Project)
	}
}

func TestDefaultConfigDir(t *testing.T) {
	dir := DefaultConfigDir()
	if dir == "" {
		t.Skip("could not determine home directory")
	}
	if filepath.Base(dir) != ".gcphcpctl" {
		t.Errorf("expected dir to end with '.gcphcpctl', got %q", dir)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path := DefaultConfigPath()
	if path == "" {
		t.Skip("could not determine home directory")
	}
	if filepath.Base(path) != "config.yaml" {
		t.Errorf("expected path to end with 'config.yaml', got %q", path)
	}
}
