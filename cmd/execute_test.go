package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveFlagsFirstSupportsNixpacksStyleOptionOrder(t *testing.T) {
	got := moveFlagsFirst([]string{".", "--name", "my-app:dev"}, map[string]bool{"--name": true})
	want := []string{"--name", "my-app:dev", "."}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestDefaultImageNameSanitizesProjectDirectory(t *testing.T) {
	if got := defaultImageName("My cool_App!"); got != "my-cool_app:latest" {
		t.Fatalf("got %q", got)
	}
	if got := defaultImageName("..."); got != "app:latest" {
		t.Fatalf("got %q", got)
	}
}

func TestPlanCommandReportsUnsupportedProject(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := ExecuteArgs([]string{"plan", t.TempDir()}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unable to detect") {
		t.Fatalf("expected a fail-closed detection error, got %v", err)
	}
}

func TestBuildCommandStopsBeforeInvokingDockerForUnknownProject(t *testing.T) {
	root := t.TempDir()
	fakeBin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "docker-was-called")
	script := "#!/bin/sh\nprintf called > \"$DOCKER_MARKER\"\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	t.Setenv("DOCKER_MARKER", marker)
	err := ExecuteArgs([]string{"build", root, "--name", "must-not-exist:latest"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unable to detect") {
		t.Fatalf("expected unsupported-project error, got %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Docker should not run when project detection fails; marker stat error: %v", err)
	}
}
