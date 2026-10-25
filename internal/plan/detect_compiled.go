package plan

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

func goPlan(root string) (Plan, error) {
	mod := read(root, "go.mod")
	version := firstMatch(regexp.MustCompile(`(?m)^go\s+([0-9]+\.[0-9]+)`), mod, "1.23")
	p := Plan{
		Provider:        "go",
		Language:        "Go",
		Version:         version,
		BuilderImage:    "golang:" + version + "-bookworm",
		RuntimeImage:    "scratch",
		RuntimeKind:     "go",
		PackageManager:  "Go modules",
		StartCommand:    []string{"/app"},
		DependencyFiles: filesMatching(root, "go.mod", "go.sum"),
	}
	if exists(root, "main.go") {
		return p, nil
	}
	matches, _ := filepath.Glob(filepath.Join(root, "cmd", "*", "main.go"))
	if len(matches) == 0 {
		return Plan{}, errors.New("Go module has no main.go at its root or under cmd/; build stopped")
	}
	if len(matches) > 1 {
		return Plan{}, fmt.Errorf("Go module has %d command entrypoints under cmd/; the provider cannot choose one automatically", len(matches))
	}
	rel, _ := filepath.Rel(root, filepath.Dir(matches[0]))
	p.ProjectFile = "./" + filepath.ToSlash(rel)
	return p, nil
}

func rustPlan(root string) (Plan, error) {
	manifest := read(root, "Cargo.toml")
	name := firstMatch(regexp.MustCompile(`(?m)^\s*name\s*=\s*["']([A-Za-z0-9_-]+)["']`), manifest, "")
	if name == "" {
		return Plan{}, errors.New("Cargo.toml has no package name at the root; workspaces need a package manifest in the project directory")
	}
	version := firstMatch(regexp.MustCompile(`(?m)^\s*rust-version\s*=\s*["']([0-9]+\.[0-9]+)`), manifest, "1.82")
	return Plan{
		Provider:        "rust",
		Language:        "Rust",
		Version:         version,
		BuilderImage:    "rust:" + version + "-bookworm",
		RuntimeImage:    "debian:bookworm-slim",
		RuntimeKind:     "rust",
		PackageManager:  "Cargo",
		StartCommand:    []string{"app"},
		ProjectFile:     "Cargo.toml:" + name,
		DependencyFiles: filesMatching(root, "Cargo.toml", "Cargo.lock"),
	}, nil
}

func cmakePlan(root string) (Plan, error) {
	content := regexp.MustCompile(`(?m)^\s*#.*$`).ReplaceAllString(read(root, "CMakeLists.txt"), "")
	target := firstMatch(regexp.MustCompile(`(?is)add_executable\s*\(\s*([a-zA-Z0-9_.+-]+)`), content, "")
	if target == "" {
		return Plan{}, errors.New("CMakeLists.txt has no literal add_executable target; build stopped")
	}
	command := strings.Join([]string{
		`cmake -S . -B /tmp/build -DCMAKE_BUILD_TYPE=Release -DCMAKE_EXE_LINKER_FLAGS="-static -static-libgcc -static-libstdc++"`,
		"cmake --build /tmp/build --target " + target + " --parallel",
	}, " && ")
	return Plan{
		Provider:        "cmake",
		Language:        "C/C++",
		Version:         "stable",
		BuilderImage:    "debian:bookworm",
		RuntimeImage:    "scratch",
		RuntimeKind:     "cmake",
		PackageManager:  "CMake",
		ProjectFile:     target,
		BuildCommand:    command,
		StartCommand:    []string{"/app"},
		DependencyFiles: filesMatching(root, "CMakeLists.txt"),
	}, nil
}
