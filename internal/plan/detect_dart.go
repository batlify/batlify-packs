package plan

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func dartPlan(root string) (Plan, error) {
	if !exists(root, "pubspec.lock") {
		return Plan{}, errors.New("Dart project needs pubspec.lock for a reproducible dependency install")
	}
	name := firstMatch(regexp.MustCompile(`(?m)^name:\s*([a-zA-Z0-9_]+)\s*$`), read(root, "pubspec.yaml"), "")
	entryPoint, err := dartEntryPoint(root, name)
	if err != nil {
		return Plan{}, err
	}
	version := strings.TrimSpace(read(root, ".dart-version"))
	if version == "" {
		version = "stable"
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+(?:\.[0-9]+)?$|^stable$`).MatchString(version) {
		return Plan{}, fmt.Errorf("unsupported .dart-version value %q; use a stable version such as 3.13.2", version)
	}
	if version != "stable" {
		parts := regexp.MustCompile(`^([0-9]+)\.([0-9]+)`).FindStringSubmatch(version)
		major, _ := strconv.Atoi(parts[1])
		minor, _ := strconv.Atoi(parts[2])
		if major < 3 || major == 3 && minor < 10 {
			return Plan{}, fmt.Errorf("Dart %s is unsupported; the provider needs Dart 3.10 or newer for dart build cli", version)
		}
	}
	return Plan{
		Provider:           "dart",
		Language:           "Dart",
		Version:            version,
		BuilderImage:       "dart:" + version + "-sdk",
		RuntimeImage:       "debian:trixie-slim",
		RuntimeKind:        "dart",
		PackageManager:     "Pub",
		BuildContextIgnore: []string{"!bin"},
		BuildCacheTarget:   "/root/.pub-cache",
		ProjectFile:        entryPoint,
		StartCommand:       []string{"/app/bin/" + strings.TrimSuffix(filepath.Base(entryPoint), ".dart")},
		Port:               8080,
		DependencyFiles:    filesMatching(root, "pubspec.yaml", "pubspec.lock", ".dart-version"),
	}, nil
}

func dartEntryPoint(root, packageName string) (string, error) {
	for _, candidate := range []string{"bin/server.dart", "bin/main.dart"} {
		if exists(root, candidate) {
			return candidate, nil
		}
	}
	if packageName != "" {
		candidate := "bin/" + packageName + ".dart"
		if exists(root, candidate) {
			return candidate, nil
		}
	}
	entries := filesMatching(root, "bin/*.dart")
	if len(entries) == 1 {
		return entries[0], nil
	}
	if len(entries) == 0 {
		return "", errors.New("Dart project has no recognizable server entry point under bin/; expected server.dart, main.dart, or <package>.dart")
	}
	return "", errors.New("Dart project has multiple possible entry points under bin/; add bin/server.dart or bin/main.dart")
}
