package plan

import (
	"encoding/json"
	"errors"
	"regexp"
)

func denoPlan(root string) (Plan, error) {
	config := read(root, "deno.json")
	if config == "" {
		config = read(root, "deno.jsonc")
	}
	var parsed struct {
		Tasks map[string]json.RawMessage `json:"tasks"`
	}
	_ = json.Unmarshal([]byte(config), &parsed)
	var startTask string
	if raw, ok := parsed.Tasks["start"]; ok {
		_ = json.Unmarshal(raw, &startTask)
	}
	var buildTask string
	if raw, ok := parsed.Tasks["build"]; ok {
		_ = json.Unmarshal(raw, &buildTask)
	}
	entry := ""
	for _, candidate := range []string{"main.ts", "main.tsx", "server.ts", "mod.ts", "main.js", "index.ts", "index.js"} {
		if exists(root, candidate) {
			entry = candidate
			break
		}
	}
	if startTask != "" {
		matched := regexp.MustCompile(`(?:^|\s)([A-Za-z0-9_./-]+\.(?:ts|tsx|js|jsx))(?:\s|$)`).FindStringSubmatch(startTask)
		if len(matched) > 1 && exists(root, matched[1]) {
			entry = matched[1]
		}
	}
	var start []string
	if startTask != "" {
		start = []string{"deno", "task", "start"}
	} else if entry != "" {
		start = []string{"deno", "run", "--allow-all", entry}
	} else {
		return Plan{}, errors.New("Deno project has no start task or recognizable main file; build stopped")
	}
	build := ""
	if buildTask != "" {
		build = "deno task build"
	}
	return Plan{
		Provider:        "deno",
		Language:        "Deno",
		Version:         "stable",
		BuilderImage:    "denoland/deno:alpine",
		RuntimeImage:    "denoland/deno:alpine",
		RuntimeKind:     "deno",
		PackageManager:  "Deno",
		ProjectFile:     entry,
		BuildCommand:    build,
		StartCommand:    start,
		Port:            8000,
		DependencyFiles: filesMatching(root, "deno.json", "deno.jsonc", "deno.lock", "import_map.json", "import_map.jsonc"),
	}, nil
}
