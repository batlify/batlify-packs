package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
		} else if exists(root, "index.html") && js.BuildCommand != "" {
			output := nodeStaticOutput(root, pkg)
			start = []string{"nginx", "-g", "daemon off;"}
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
				StartCommand:     start,
				Port:             8080,
				DependencyFiles:  js.DependencyFiles,
				InstallAfterCopy: js.InstallAfterCopy,
				BuildCacheTarget: js.CacheTarget,
				RuntimeOutputDir: output,
			}, nil
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
		angular := read(root, "angular.json")
		output := firstMatch(regexp.MustCompile(`"outputPath"\s*:\s*"([^"]+)"`), angular, "dist")
		return output
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

func hasNodeDependency(pkg nodeManifest, name string) bool {
	if _, ok := pkg.Dependencies[name]; ok {
		return true
	}
	_, ok := pkg.DevDeps[name]
	return ok
}
