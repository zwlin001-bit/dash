package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveExecPath_AbsoluteExists(t *testing.T) {
	// /bin/sh or similar should exist on any unix
	shPath := "/bin/sh"
	if _, err := os.Stat(shPath); err != nil {
		shPath = "/usr/bin/sh"
	}
	res, err := ResolveExecPath(shPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != shPath {
		t.Errorf("expected %q, got %q", shPath, res)
	}
}

func TestResolveExecPath_RelativeInPATH(t *testing.T) {
	// Create a temp directory with an executable
	tmpDir := t.TempDir()
	exePath := filepath.Join(tmpDir, "dummy-test-provider")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	origPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })
	_ = os.Setenv("PATH", tmpDir+":"+origPath)

	// Resolve by base name
	res, err := ResolveExecPath("dummy-test-provider")
	if err != nil {
		t.Fatalf("unexpected error resolving by PATH: %v", err)
	}
	if filepath.Base(res) != "dummy-test-provider" {
		t.Errorf("expected dummy-test-provider, got %s", res)
	}

	// Resolve by relative path with prefix (e.g. bin/dummy-test-provider)
	res2, err := ResolveExecPath("bin/dummy-test-provider")
	if err != nil {
		t.Fatalf("unexpected error resolving relative path: %v", err)
	}
	if filepath.Base(res2) != "dummy-test-provider" {
		t.Errorf("expected dummy-test-provider, got %s", res2)
	}
}

func TestResolveExecPath_RelativeNearSelf(t *testing.T) {
	selfExe, err := os.Executable()
	if err != nil {
		t.Skip("cannot determine self executable")
	}
	selfDir := filepath.Dir(selfExe)

	// Put a dummy binary in selfDir
	exePath := filepath.Join(selfDir, "test-self-provider")
	if err := os.WriteFile(exePath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Skip("cannot write to selfDir:", err)
	}
	defer func() { _ = os.Remove(exePath) }()

	// Resolve by relative path
	res, err := ResolveExecPath("test-self-provider")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != exePath {
		t.Errorf("expected %s, got %s", exePath, res)
	}
}

func TestResolveExecPath_NonExistent(t *testing.T) {
	_, err := ResolveExecPath("completely-non-existent-binary-xyz-123")
	if err == nil {
		t.Fatal("expected error for non-existent binary, got nil")
	}
}
