package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type composerManifest struct {
	Require map[string]string `json:"require"`
	Config  struct {
		Platform map[string]string `json:"platform"`
	} `json:"config"`
}

func rubyPlan(root string) (Plan, error) {
	version := firstMatch(regexp.MustCompile(`(?:^|v)([0-9]+\.[0-9]+)`), read(root, ".ruby-version"), "3.3")
	gemfile := read(root, "Gemfile")
	version = firstMatch(regexp.MustCompile(`(?m)^\s*ruby\s+["']([0-9]+\.[0-9]+)`), gemfile, version)
	var start []string
	port := 3000
	if exists(root, "config.ru") {
		start = []string{"bundle", "exec", "rackup", "--host", "0.0.0.0", "-p", "3000"}
	} else if exists(root, "config/application.rb") {
		start = []string{"bundle", "exec", "rails", "server", "-b", "0.0.0.0", "-p", "3000"}
	} else if exists(root, "app.rb") {
		start = []string{"ruby", "app.rb"}
	} else {
		return Plan{}, errors.New("Ruby project has no config.ru, Rails application, or app.rb start target; build stopped")
	}
	return Plan{
		Provider:        "ruby",
		Language:        "Ruby",
		Version:         version,
		BuilderImage:    "ruby:" + version + "-bookworm",
		RuntimeImage:    "ruby:" + version + "-slim-bookworm",
		RuntimeKind:     "ruby",
		PackageManager:  "Bundler",
		InstallCommand:  "bundle config set without 'development test' && bundle install",
		StartCommand:    start,
		Port:            port,
		DependencyFiles: filesMatching(root, "Gemfile", "Gemfile.lock", ".ruby-version"),
	}, nil
}

func phpPlan(root string) (Plan, error) {
	composer, err := readComposerManifest(root)
	if err != nil {
		return Plan{}, err
	}
	version := phpVersion(composer)
	port := 8080
	webRoot := "public"
	if _, err := os.Stat(filepath.Join(root, "public", "index.php")); err != nil {
		if !exists(root, "index.php") {
			return Plan{}, errors.New("PHP project has no public/index.php or index.php start target; build stopped")
		}
		webRoot = "."
	}
	start := []string{"php", "-S", "0.0.0.0:8080", "-t", webRoot}
	install := ""
	if exists(root, "composer.json") {
		install = "composer install --no-dev --no-interaction --prefer-dist --optimize-autoloader"
	}
	return Plan{
		Provider:        "php",
		Language:        "PHP",
		Version:         version,
		BuilderImage:    "php:" + version + "-cli-bookworm",
		RuntimeImage:    "php:" + version + "-cli-bookworm",
		RuntimeKind:     "php",
		PackageManager:  "Composer",
		InstallCommand:  install,
		StartCommand:    start,
		Port:            port,
		DependencyFiles: filesMatching(root, "composer.json", "composer.lock"),
	}, nil
}

func laravelPlan(root string, composer composerManifest) (Plan, error) {
	var frontend *JavaScriptBuild
	var buildContextIgnore []string
	if exists(root, "package.json") {
		var manifest nodeManifest
		if err := json.Unmarshal([]byte(read(root, "package.json")), &manifest); err != nil {
			return Plan{}, fmt.Errorf("invalid package.json for Laravel frontend: %w", err)
		}
		if hasNodeDependency(manifest, "next") {
			return Plan{}, errors.New("a Next.js server cannot be embedded in Laravel's public/build assets; build the frontend and backend as separate images")
		}
		config := detectJavaScriptBuild(root, manifest)
		if err := requireSupportedJavaScriptBuild(config); err != nil {
			return Plan{}, err
		}
		if config.BuildCommand == "" {
			return Plan{}, errors.New("Laravel project contains package.json but no build script; add a frontend build script or remove package.json")
		}
		frontend = &config
		buildContextIgnore = []string{"public/build"}
	}

	version := phpVersion(composer)
	return Plan{
		Provider:           "laravel",
		Language:           "PHP (Laravel)",
		Version:            version,
		BuilderImage:       "php:" + version + "-cli-bookworm",
		RuntimeImage:       "php:" + version + "-cli-bookworm",
		RuntimeKind:        "laravel",
		PackageManager:     "Composer",
		InstallCommand:     "composer install --no-dev --no-interaction --prefer-dist --no-scripts --no-autoloader",
		BuildCommand:       "composer dump-autoload --no-dev --optimize",
		StartCommand:       []string{"php", "-S", "0.0.0.0:8080", "-t", "public", ".batlify-packs-router.php"},
		Port:               8080,
		DependencyFiles:    filesMatching(root, "composer.json", "composer.lock"),
		FrontendBuild:      frontend,
		BuildContextIgnore: buildContextIgnore,
	}, nil
}

func detectLaravelProject(root string) (composerManifest, bool) {
	if !exists(root, "artisan") || !exists(root, "public/index.php") {
		return composerManifest{}, false
	}
	composer, err := readComposerManifest(root)
	if err != nil {
		return composerManifest{}, false
	}
	_, found := composer.Require["laravel/framework"]
	return composer, found
}

func readComposerManifest(root string) (composerManifest, error) {
	var composer composerManifest
	if !exists(root, "composer.json") {
		return composer, nil
	}
	if err := json.Unmarshal([]byte(read(root, "composer.json")), &composer); err != nil {
		return composerManifest{}, fmt.Errorf("invalid composer.json: %w", err)
	}
	return composer, nil
}

func phpVersion(composer composerManifest) string {
	if version := firstMatch(regexp.MustCompile(`^([0-9]+\.[0-9]+)`), composer.Config.Platform["php"], ""); version != "" {
		return version
	}
	minimum := firstMatch(regexp.MustCompile(`([0-9]+\.[0-9]+)`), composer.Require["php"], "")
	if minimum == "" {
		return "8.3"
	}
	parts := strings.SplitN(minimum, ".", 2)
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr == nil && minorErr == nil && (major > 8 || major == 8 && minor > 3) {
		return minimum
	}
	return "8.3"
}

func elixirPlan(root string) (Plan, error) {
	manifest := read(root, "mix.exs")
	version := firstMatch(regexp.MustCompile(`~>\s*([0-9]+\.[0-9]+)`), manifest, "1.17")
	app := firstMatch(regexp.MustCompile(`(?m)\bapp:\s*:([a-zA-Z0-9_]+)`), manifest, "")
	if app == "" {
		return Plan{}, errors.New("could not infer the Elixir application name from mix.exs")
	}
	if !anyExists(root, "mix.lock") {
		return Plan{}, errors.New("Elixir project needs mix.lock for a reproducible dependency install")
	}
	return Plan{
		Provider:        "elixir",
		Language:        "Elixir",
		Version:         version,
		BuilderImage:    "elixir:" + version + "-slim",
		RuntimeImage:    "debian:bookworm-slim",
		RuntimeKind:     "elixir",
		PackageManager:  "Mix",
		ProjectFile:     app,
		InstallCommand:  "mix deps.get --only prod",
		BuildCommand:    "MIX_ENV=prod mix release",
		StartCommand:    []string{"/app/bin/" + app, "start"},
		Port:            4000,
		DependencyFiles: filesMatching(root, "mix.exs", "mix.lock"),
	}, nil
}
