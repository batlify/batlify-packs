package build

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/batlify/batlify-packs/internal/plan"
)

func TestWriteContextTarFiltersDefaultsAndAppliesDockerignore(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"main.js":                    "console.log('ok')",
		"Dockerfile":                 "FROM user-image\n",
		".env":                       "SECRET=hidden",
		"node_modules/pkg/index.js":  "not sent",
		".next/server/server.js":     "stale Next.js output",
		"vendor/autoload.php":        "installed Composer dependency",
		"public/build/manifest.json": "stale Laravel frontend output",
		"debug.log":                  "ignored by user",
		"keep.log":                   "explicitly included",
		".dockerignore":              "*.log\n!keep.log\n",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	if err := writeContextTar(&archive, root, generatedDockerfile, "FROM scratch\n", "public/build"); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(&archive)
	var names []string
	var dockerIgnoreContents, dockerfileContents string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
		if header.Name == ".dockerignore" {
			contents, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			dockerIgnoreContents = string(contents)
		}
		if header.Name == generatedDockerfile {
			contents, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			dockerfileContents = string(contents)
		}
	}
	seen := make(map[string]bool)
	for _, name := range names {
		seen[name] = true
	}
	for _, name := range []string{"main.js", ".dockerignore", "keep.log", generatedDockerfile} {
		if !seen[name] {
			t.Errorf("expected context to contain %q; got %v", name, names)
		}
	}
	for _, name := range []string{".env", "node_modules/pkg/index.js", ".next/server/server.js", "vendor/autoload.php", "public/build/manifest.json", "debug.log"} {
		if seen[name] {
			t.Errorf("expected context to exclude %q", name)
		}
	}
	if !strings.Contains(dockerIgnoreContents, "public/build\n*.log\n!keep.log\nDockerfile\n.dockerignore\n") {
		t.Fatalf("context ignore file did not preserve project rules and exclude build files: %q", dockerIgnoreContents)
	}
	if dockerfileContents != "FROM scratch\n" {
		t.Fatalf("generated Dockerfile was replaced by the project's Dockerfile: %q", dockerfileContents)
	}
}

func TestGlobRegexSupportsDoubleStar(t *testing.T) {
	rx, err := globRegex("**/*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"a.tmp", "a/b.tmp", "a/b/c.tmp"} {
		if !rx.MatchString(path) {
			t.Errorf("pattern did not match %q", path)
		}
	}
}

func TestBuildStreamsGeneratedDockerfileThroughDockerCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Docker CLI uses a POSIX shell")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeBin := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "context.tar")
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGS_PATH\"\n/usr/bin/cat > \"$CAPTURE_PATH\"\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	t.Setenv("CAPTURE_PATH", archivePath)
	t.Setenv("ARGS_PATH", argsPath)
	p, err := plan.Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := BuildWithOptions(p, "test-image:latest", io.Discard, io.Discard, Options{NoCache: true}); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--no-cache") {
		t.Fatalf("Docker build did not receive --no-cache: %q", args)
	}
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(bytes.NewReader(data))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == generatedDockerfile {
			dockerfile, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(dockerfile, []byte("FROM docker.io/nginxinc/nginx-unprivileged:alpine")) {
				t.Fatalf("archive contains wrong generated Dockerfile:\n%s", dockerfile)
			}
			return
		}
	}
	t.Fatal("generated Dockerfile was missing from streamed Docker build context")
}

func TestBuildSurfacesDockerErrorsWithoutPipeNoise(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Docker CLI uses a POSIX shell")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeBin := t.TempDir()
	script := "#!/bin/sh\necho 'daemon unavailable' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	p, err := plan.Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	err = Build(p, "test-image:latest", io.Discard, &stderr)
	if err == nil || !strings.Contains(err.Error(), "Docker image build failed") {
		t.Fatalf("expected Docker build failure, got %v", err)
	}
	if strings.Contains(err.Error(), "closed pipe") || !strings.Contains(stderr.String(), "daemon unavailable") {
		t.Fatalf("Docker failure was obscured: err=%v stderr=%q", err, stderr.String())
	}
}
