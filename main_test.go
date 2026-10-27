package main

import (
	"path/filepath"
	"testing"
)

func TestDefaultConfigPathPrefersXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if got, want := defaultConfigPath(), filepath.Join(dir, "tempo", "tempo.toml"); got != want {
		t.Errorf("defaultConfigPath() = %q, want %q", got, want)
	}
}

func TestDefaultConfigPathFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	if got, want := defaultConfigPath(), filepath.Join(home, ".config", "tempo", "tempo.toml"); got != want {
		t.Errorf("defaultConfigPath() = %q, want %q", got, want)
	}
}

func TestDefaultLogPathPrefersXDGStateHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if got, want := defaultLogPath(), filepath.Join(dir, "tempo", "tempo.log"); got != want {
		t.Errorf("defaultLogPath() = %q, want %q", got, want)
	}
}

func TestDefaultLogPathFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", home)
	if got, want := defaultLogPath(), filepath.Join(home, ".local", "state", "tempo", "tempo.log"); got != want {
		t.Errorf("defaultLogPath() = %q, want %q", got, want)
	}
}
