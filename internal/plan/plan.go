package plan

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Plan is the stable, inspectable description of the build that is also used
// to render the temporary Dockerfile passed to Docker BuildKit.
type Plan struct {
	ProjectDir         string           `json:"projectDir"`
	Provider           string           `json:"provider"`
	Language           string           `json:"language"`
	Version            string           `json:"version"`
	BuilderImage       string           `json:"builderImage"`
	RuntimeImage       string           `json:"runtimeImage"`
	PackageManager     string           `json:"packageManager,omitempty"`
	BuildCommand       string           `json:"buildCommand,omitempty"`
	StartCommand       []string         `json:"startCommand"`
	Port               int              `json:"port,omitempty"`
	DependencyFiles    []string         `json:"dependencyFiles,omitempty"`
	ProjectFile        string           `json:"projectFile,omitempty"`
	RuntimeOutputDir   string           `json:"runtimeOutputDir,omitempty"`
	FrontendBuild      *JavaScriptBuild `json:"frontendBuild,omitempty"`
	NextStandalone     bool             `json:"nextStandalone,omitempty"`
	BuildCacheTarget   string           `json:"-"`
	RuntimeKind        string           `json:"-"`
	InstallCommand     string           `json:"-"`
	ProductionCommand  string           `json:"-"`
	InstallAfterCopy   bool             `json:"-"`
	RuntimeVenv        bool             `json:"-"`
	BuildContextIgnore []string         `json:"-"`
}

// Dockerfile returns the internal multi-stage Dockerfile. It intentionally
// keeps compilers and package-manager caches out of the final image whenever
// the ecosystem provides a separate runtime image.
func (p Plan) Dockerfile() (string, error) {
	var b strings.Builder
	b.WriteString("# syntax=docker/dockerfile:1.7\n")
	switch p.RuntimeKind {
	case "node":
		writeNodeDockerfile(&b, p)
	case "bun":
		writeBunDockerfile(&b, p)
	case "deno":
		writeDenoDockerfile(&b, p)
	case "cmake":
		writeCMakeDockerfile(&b, p)
	case "node-static":
		writeNodeStaticDockerfile(&b, p)
	case "python":
		writePythonDockerfile(&b, p)
	case "go":
		writeGoDockerfile(&b, p)
	case "rust":
		writeRustDockerfile(&b, p)
	case "java":
		writeJavaDockerfile(&b, p)
	case "dotnet":
		writeDotnetDockerfile(&b, p)
	case "ruby":
		writeRubyDockerfile(&b, p)
	case "php":
		writePHPDockerfile(&b, p)
	case "laravel":
		writeLaravelDockerfile(&b, p)
	case "elixir":
		writeElixirDockerfile(&b, p)
	case "static":
		writeStaticDockerfile(&b)
	default:
		return "", fmt.Errorf("no Dockerfile recipe for provider %q", p.Provider)
	}
	return qualifyDockerfileImages(b.String()), nil
}

func qualifyDockerfileImages(recipe string) string {
	lines := strings.Split(recipe, "\n")
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		imageIndex := 1
		if strings.HasPrefix(fields[imageIndex], "--platform=") {
			imageIndex++
		}
		if imageIndex >= len(fields) {
			continue
		}
		fields[imageIndex] = qualifyImageReference(fields[imageIndex])
		lines[i] = strings.Join(fields, " ")
	}
	return strings.Join(lines, "\n")
}

func qualifyImageReference(image string) string {
	if image == "scratch" || strings.Contains(image, "@") {
		return image
	}
	first, _, hasPath := strings.Cut(image, "/")
	if !hasPath {
		return "docker.io/library/" + image
	}
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return image
	}
	return "docker.io/" + image
}

// buildStageSpec describes the common setup shared by source build stages.
// InstallAfterCopy is used when dependency installation needs workspace files.
type buildStageSpec struct {
	image            string
	name             string
	user             string
	workdir          string
	environment      []string
	dependencyFiles  []string
	installCommand   string
	installCache     string
	installAfterCopy bool
	buildCommand     string
	buildCache       string
}

func writeBuildStage(b *strings.Builder, spec buildStageSpec) {
	fmt.Fprintf(b, "FROM %s AS %s\n", spec.image, spec.name)
	if spec.user != "" {
		fmt.Fprintf(b, "USER %s\n", spec.user)
	}
	for _, variable := range spec.environment {
		fmt.Fprintf(b, "ENV %s\n", variable)
	}
	if spec.workdir != "" {
		fmt.Fprintf(b, "WORKDIR %s\n", spec.workdir)
	}
	for _, file := range spec.dependencyFiles {
		writeManifestCopy(b, file)
	}
	if spec.installCommand != "" && !spec.installAfterCopy {
		writeRun(b, spec.installCache, spec.installCommand)
	}
	b.WriteString("COPY . .\n")
	if spec.installCommand != "" && spec.installAfterCopy {
		writeRun(b, spec.installCache, spec.installCommand)
	}
	if spec.buildCommand != "" {
		writeRun(b, spec.buildCache, spec.buildCommand)
	}
}

func writeRun(b *strings.Builder, cacheTarget, command string) {
	if cacheTarget == "" {
		fmt.Fprintf(b, "RUN %s\n", command)
		return
	}
	fmt.Fprintf(b, "RUN --mount=type=cache,target=%s %s\n", cacheTarget, command)
}

func writeExposeAndCommand(b *strings.Builder, p Plan) {
	if p.Port != 0 {
		fmt.Fprintf(b, "EXPOSE %d\n", p.Port)
	}
	writeJSONCommand(b, p.StartCommand)
}

func writeJSONCommand(b *strings.Builder, command []string) {
	data, _ := json.Marshal(command)
	fmt.Fprintf(b, "CMD %s\n", data)
}

func dockerPath(s string) string {
	return strings.NewReplacer("\\", "/", "\r", "", "\n", "").Replace(s)
}

func dockerJSON(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func writeManifestCopy(b *strings.Builder, path string) {
	path = dockerPath(path)
	quoted := dockerJSON(path)
	fmt.Fprintf(b, "COPY [%s, %s]\n", quoted, quoted)
}

func nodeCacheTarget(manager string) string {
	switch manager {
	case "pnpm":
		return "/root/.local/share/pnpm/store"
	case "yarn":
		return "/usr/local/share/.cache/yarn"
	default:
		return "/root/.npm"
	}
}
