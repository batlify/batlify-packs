package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

var nextStandaloneOutput = regexp.MustCompile(`(?m)(?:^[ \t]*|[,{][ \t]*)(?:["']?output["']?)[ \t]*:[ \t]*["']standalone["']`)

type nodeManifest struct {
	Name         string            `json:"name"`
	Main         string            `json:"main"`
	Scripts      map[string]string `json:"scripts"`
	Dependencies map[string]string `json:"dependencies"`
	DevDeps      map[string]string `json:"devDependencies"`
	Engines      map[string]string `json:"engines"`
	PackageMgr   string            `json:"packageManager"`
	Workspaces   json.RawMessage   `json:"workspaces"`
}

func nodePlan(root string) (Plan, error) {
	var pkg nodeManifest
	if err := json.Unmarshal([]byte(read(root, "package.json")), &pkg); err != nil {
		return Plan{}, fmt.Errorf("invalid package.json: %w", err)
	}
	_, nextInDependencies := pkg.Dependencies["next"]
	_, nextInDevDependencies := pkg.DevDeps["next"]
	if nextInDevDependencies && !nextInDependencies {
		return Plan{}, errors.New("Next.js is listed only in devDependencies and would be removed from the runtime image; add it to dependencies")
	}
	if (nextInDependencies || nextInDevDependencies) && pkg.Scripts["build"] == "" {
		return Plan{}, errors.New("Next.js project has no build script; add a production build command to package.json")
	}
	js := detectJavaScriptBuild(root, pkg)
	if err := requireSupportedJavaScriptBuild(js); err != nil {
		return Plan{}, err
	}
	nextStandalone := nextInDependencies &&
		js.PackageManager != "bun" &&
		usesNextStartScript(pkg.Scripts["start"]) &&
		nextStandaloneConfigured(root)
	if nextStandalone {
		js.ProductionCommand = "mkdir -p .next/standalone/.next && if [ -d .next/static ]; then cp -a .next/static .next/standalone/.next/static; fi && if [ -d public ]; then cp -a public .next/standalone/public; fi"
	}
	if isNodeStaticFrontend(root, pkg) {
		return Plan{
			Provider:         "node",
			Language:         "Node.js static frontend",
			Version:          js.Version,
			BuilderImage:     js.BuilderImage,
			RuntimeImage:     "nginxinc/nginx-unprivileged:alpine",
			RuntimeKind:      "node-static",
			PackageManager:   js.PackageManager,
			InstallCommand:   js.InstallCommand,
			BuildCommand:     js.BuildCommand,
			StartCommand:     []string{"nginx", "-g", "daemon off;"},
			Port:             8080,
			DependencyFiles:  js.DependencyFiles,
			InstallAfterCopy: js.InstallAfterCopy,
			BuildCacheTarget: js.CacheTarget,
			RuntimeOutputDir: nodeStaticOutput(root, pkg),
		}, nil
	}
	start := []string{"npm", "start"}
	if js.PackageManager == "bun" {
		start = []string{"bun", "run", "start"}
	}
	port := 3000
	if _, ok := pkg.Scripts["start"]; !ok {
		if _, ok := pkg.Dependencies["next"]; ok {
			start = []string{"node_modules/.bin/next", "start", "-H", "0.0.0.0", "-p", "3000"}
		} else if _, ok := pkg.DevDeps["next"]; ok {
			start = []string{"node_modules/.bin/next", "start", "-H", "0.0.0.0", "-p", "3000"}
		} else {
			entry := pkg.Main
			if entry == "" {
				for _, candidate := range []string{"server.js", "app.js", "index.js"} {
					if exists(root, candidate) {
						entry = candidate
						break
					}
				}
			}
			if entry == "" || strings.ContainsAny(entry, "\r\n\x00") {
				return Plan{}, errors.New("Node.js project has no start script, Next.js server, or recognizable main file; build stopped")
			}
			start = []string{"node", entry}
			if js.PackageManager == "bun" {
				start = []string{"bun", entry}
			}
		}
	}
	if nextStandalone {
		start = []string{"node", "server.js"}
	}
	runtimeImage := js.BuilderImage
	runtimeKind := "node"
	language := "Node.js"
	if js.PackageManager == "bun" {
		runtimeKind, language = "bun", "Node.js (Bun)"
	}
	return Plan{
		Provider:          "node",
		Language:          language,
		Version:           js.Version,
		BuilderImage:      js.BuilderImage,
		RuntimeImage:      runtimeImage,
		RuntimeKind:       runtimeKind,
		PackageManager:    js.PackageManager,
		InstallCommand:    js.InstallCommand,
		ProductionCommand: js.ProductionCommand,
		NextStandalone:    nextStandalone,
		BuildCommand:      js.BuildCommand,
		StartCommand:      start,
		Port:              port,
		DependencyFiles:   js.DependencyFiles,
		InstallAfterCopy:  js.InstallAfterCopy,
		BuildCacheTarget:  js.CacheTarget,
	}, nil
}

func isNodeStaticFrontend(root string, pkg nodeManifest) bool {
	if pkg.Scripts["build"] == "" || hasNodeDependency(pkg, "next") {
		return false
	}
	start := strings.Fields(pkg.Scripts["start"])
	if len(start) == 0 {
		return exists(root, "index.html") || hasAnyNodeDependency(pkg,
			"react-scripts", "gatsby", "@angular/cli", "@vue/cli-service", "astro")
	}
	switch {
	case hasNodeDependency(pkg, "react-scripts"):
		return commandPair(start, "react-scripts", "start")
	case hasNodeDependency(pkg, "gatsby"):
		return commandPair(start, "gatsby", "develop")
	case hasNodeDependency(pkg, "@angular/cli"):
		return commandPair(start, "ng", "serve")
	case hasNodeDependency(pkg, "@vue/cli-service"):
		return commandPair(start, "vue-cli-service", "serve")
	case hasNodeDependency(pkg, "astro"):
		return commandPair(start, "astro", "dev")
	case hasNodeDependency(pkg, "vite"):
		return exists(root, "index.html") && (start[0] == "vite" || commandPair(start, "vite", "preview"))
	default:
		return false
	}
}

func commandPair(command []string, executable, subcommand string) bool {
	for index := 0; index+1 < len(command); index++ {
		if command[index] == executable && command[index+1] == subcommand {
			return true
		}
	}
	return false
}

func hasAnyNodeDependency(pkg nodeManifest, names ...string) bool {
	for _, name := range names {
		if hasNodeDependency(pkg, name) {
			return true
		}
	}
	return false
}

func nextStandaloneConfigured(root string) bool {
	for _, config := range []string{"next.config.js", "next.config.mjs", "next.config.cjs", "next.config.ts"} {
		if nextStandaloneOutput.MatchString(read(root, config)) {
			return true
		}
	}
	return false
}

func usesNextStartScript(script string) bool {
	if strings.TrimSpace(script) == "" {
		return true
	}
	fields := strings.Fields(script)
	for i := 0; i+1 < len(fields); i++ {
		if strings.TrimSuffix(fields[i], ".cmd") == "next" && fields[i+1] == "start" {
			return true
		}
	}
	return false
}

func installAfterCopyCandidate(root string, pkg nodeManifest) bool {
	return exists(root, "pnpm-workspace.yaml") || exists(root, "lerna.json") || len(pkg.Workspaces) > 0
}

func nodeStaticOutput(root string, pkg nodeManifest) string {
	if hasNodeDependency(pkg, "react-scripts") {
		return "build"
	}
	if hasNodeDependency(pkg, "gatsby") {
		return "public"
	}
	if hasNodeDependency(pkg, "@angular/cli") {
		return angularOutputDirectory(root, pkg.Name)
	}
	if hasNodeDependency(pkg, "vite") {
		return "dist"
	}
	if hasNodeDependency(pkg, "@vue/cli-service") {
		return "dist"
	}
	if hasNodeDependency(pkg, "astro") {
		return "dist"
	}
	return "dist"
}

func angularOutputDirectory(root, packageName string) string {
	var workspace struct {
		Projects map[string]struct {
			Architect map[string]struct {
				Builder string `json:"builder"`
				Options struct {
					OutputPath json.RawMessage `json:"outputPath"`
				} `json:"options"`
			} `json:"architect"`
			Targets map[string]struct {
				Builder string `json:"builder"`
				Options struct {
					OutputPath json.RawMessage `json:"outputPath"`
				} `json:"options"`
			} `json:"targets"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(read(root, "angular.json")), &workspace); err != nil {
		return "dist"
	}
	projectNames := make([]string, 0, len(workspace.Projects))
	for name := range workspace.Projects {
		projectNames = append(projectNames, name)
	}
	sort.Strings(projectNames)
	if _, ok := workspace.Projects[packageName]; ok {
		projectNames = append([]string{packageName}, slices.DeleteFunc(projectNames, func(name string) bool { return name == packageName })...)
	}
	for _, name := range projectNames {
		project := workspace.Projects[name]
		target, ok := project.Architect["build"]
		if !ok {
			target, ok = project.Targets["build"]
		}
		if !ok {
			continue
		}
		var base string
		var browser string
		if err := json.Unmarshal(target.Options.OutputPath, &base); err == nil {
			if strings.HasSuffix(target.Builder, ":application") {
				browser = "browser"
			}
		} else {
			var output struct {
				Base    string `json:"base"`
				Browser string `json:"browser"`
			}
			if err := json.Unmarshal(target.Options.OutputPath, &output); err != nil {
				continue
			}
			base, browser = output.Base, output.Browser
		}
		if base == "" {
			base = filepath.Join("dist", name)
		}
		if browser != "" {
			base = filepath.Join(base, browser)
		}
		return filepath.ToSlash(base)
	}
	return "dist"
}

func hasNodeDependency(pkg nodeManifest, name string) bool {
	if _, ok := pkg.Dependencies[name]; ok {
		return true
	}
	_, ok := pkg.DevDeps[name]
	return ok
}
