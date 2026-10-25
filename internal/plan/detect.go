package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type providerDetector struct {
	name       string
	matches    bool
	createPlan func(string) (Plan, error)
}

func Detect(root string) (Plan, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Plan{}, fmt.Errorf("read project path %q: %w", root, err)
	}
	if !info.IsDir() {
		return Plan{}, fmt.Errorf("project path %q is not a directory", root)
	}

	laravelManifest, laravel := detectLaravelProject(abs)
	detectors := []providerDetector{
		{
			name:    "laravel",
			matches: laravel,
			createPlan: func(root string) (Plan, error) {
				return laravelPlan(root, laravelManifest)
			},
		},
		{name: "node", matches: exists(abs, "package.json") && !laravel, createPlan: nodePlan},
		{name: "deno", matches: anyExists(abs, "deno.json", "deno.jsonc"), createPlan: denoPlan},
		{
			name:       "python",
			matches:    anyExists(abs, "pyproject.toml", "requirements.txt", "requirements-prod.txt", "Pipfile", "setup.py", "manage.py", "main.py", "app.py"),
			createPlan: pythonPlan,
		},
		{name: "go", matches: exists(abs, "go.mod"), createPlan: goPlan},
		{name: "rust", matches: exists(abs, "Cargo.toml"), createPlan: rustPlan},
		{name: "dotnet", matches: anyGlob(abs, "*.csproj", "*.fsproj", "*.vbproj"), createPlan: dotnetPlan},
		{name: "java", matches: anyExists(abs, "pom.xml", "build.gradle", "build.gradle.kts"), createPlan: javaPlan},
		{name: "ruby", matches: exists(abs, "Gemfile"), createPlan: rubyPlan},
		{name: "php", matches: (anyExists(abs, "composer.json", "index.php") || exists(abs, "public/index.php")) && !laravel, createPlan: phpPlan},
		{name: "elixir", matches: exists(abs, "mix.exs"), createPlan: elixirPlan},
		{name: "cmake", matches: exists(abs, "CMakeLists.txt"), createPlan: cmakePlan},
		{name: "static", matches: exists(abs, "index.html"), createPlan: staticPlan},
	}
	var selected []providerDetector
	for _, detector := range detectors {
		if detector.matches {
			selected = append(selected, detector)
		}
	}
	// A package.json plus root index.html is the normal shape of a frontend
	// project; let the Node provider decide whether it is a static build.
	if len(selected) > 1 && exists(abs, "package.json") {
		filtered := selected[:0]
		for _, detector := range selected {
			if detector.name != "static" {
				filtered = append(filtered, detector)
			}
		}
		selected = filtered
	}
	if len(selected) == 0 {
		return Plan{}, fmt.Errorf("unable to detect a supported project in %q; build stopped without creating an image", abs)
	}
	if len(selected) > 1 {
		names := make([]string, 0, len(selected))
		for _, detector := range selected {
			names = append(names, detector.name)
		}
		sort.Strings(names)
		return Plan{}, fmt.Errorf(
			"ambiguous project in %q (detected %s); build stopped without creating an image",
			abs,
			strings.Join(names, ", "),
		)
	}
	p, err := selected[0].createPlan(abs)
	if err != nil {
		return Plan{}, err
	}
	p.ProjectDir = abs
	return p, nil
}

func exists(root, name string) bool {
	info, err := os.Stat(filepath.Join(root, name))
	return err == nil && !info.IsDir()
}

func anyExists(root string, names ...string) bool {
	for _, name := range names {
		if exists(root, name) {
			return true
		}
	}
	return false
}

func anyGlob(root string, patterns ...string) bool {
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		if len(matches) > 0 {
			return true
		}
	}
	return false
}

func filesMatching(root string, patterns ...string) []string {
	var files []string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, match := range matches {
			if info, err := os.Stat(match); err == nil && !info.IsDir() {
				relative, relErr := filepath.Rel(root, match)
				if relErr == nil {
					files = append(files, filepath.ToSlash(relative))
				}
			}
		}
	}
	sort.Strings(files)
	return slices.Compact(files)
}

func read(root, name string) string {
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return ""
	}
	return string(b)
}

func firstMatch(re *regexp.Regexp, text, fallback string) string {
	match := re.FindStringSubmatch(text)
	if len(match) > 1 {
		return match[1]
	}
	return fallback
}
