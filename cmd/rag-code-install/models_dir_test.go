package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// populate makes dir look like an Ollama model store.
func populate(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "models", "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func setModelsDir(t *testing.T, v string) {
	t.Helper()
	orig := *modelsDir
	*modelsDir = v
	t.Cleanup(func() { *modelsDir = orig })
}

// isolate points HOME at a temp dir and clears the service locations, so the
// result does not depend on what the machine running the tests has installed.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads this on Windows
	orig := linuxServiceModelDirs
	linuxServiceModelDirs = nil
	t.Cleanup(func() { linuxServiceModelDirs = orig })
	return home
}

func TestHostModelsDirUsesFlagAndCreatesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "models-home")
	setModelsDir(t, dir)

	got, err := hostModelsDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("hostModelsDir() = %q, want %q", got, dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatal("the models dir should be created so the mount is shared with the host")
	}
}

func TestHostModelsDirFallsBackToHome(t *testing.T) {
	home := isolate(t)
	setModelsDir(t, "")

	got, err := hostModelsDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".ollama"); got != want {
		t.Fatalf("hostModelsDir() = %q, want %q", got, want)
	}
}

func TestHostModelsDirPrefersPopulatedServiceDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("service model dirs are Linux-only")
	}
	isolate(t)
	setModelsDir(t, "")
	// An Ollama installed as a systemd service keeps its models outside $HOME;
	// defaulting to the empty ~/.ollama would re-download several GB.
	service := populate(t, filepath.Join(t.TempDir(), "service"))
	linuxServiceModelDirs = []string{service}

	got, err := hostModelsDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != service {
		t.Fatalf("hostModelsDir() = %q, want the populated service dir %q", got, service)
	}
}

func TestHasModels(t *testing.T) {
	if !hasModels(populate(t, t.TempDir())) {
		t.Error("a dir containing models/ should be detected")
	}
	if hasModels(t.TempDir()) {
		t.Error("an empty dir should not be detected")
	}
	if hasModels(filepath.Join(t.TempDir(), "missing")) {
		t.Error("a missing dir should not be detected")
	}
}
