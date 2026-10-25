package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectNodeAndRenderLeanRuntime(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name":"demo","scripts":{"build":"vite build","start":"node server.js"},"engines":{"node":">=20"}}`)
	writeFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	writeFile(t, root, "server.js", `console.log("ready")`)
	p, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Provider != "node" || p.Version != "20" || p.PackageManager != "npm" {
		t.Fatalf("unexpected plan: %#v", p)
	}
	dockerfile, err := p.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	requiredFragments := []string{
		"FROM docker.io/library/node:20-bookworm-slim AS build",
		"FROM docker.io/library/node:20-bookworm-slim AS runtime",
		"npm ci --no-audit --no-fund",
		"npm run build",
		"npm prune --omit=dev",
		"USER node",
		`CMD ["npm","start"]`,
	}
	for _, required := range requiredFragments {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile missing %q:\n%s", required, dockerfile)
		}
	}
}

func TestDetectLaravelWithReactFrontend(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "composer.json", `{"require":{"php":"^8.2","laravel/framework":"^11.0"}}`)
	writeFile(t, root, "composer.lock", "{}")
	writeFile(t, root, "artisan", "<?php\n")
	writeFile(t, root, "public/index.php", "<?php\n")
	writeFile(t, root, "package.json", `{"scripts":{"build":"vite build"},"dependencies":{"react":"^18.0.0","react-dom":"^18.0.0"},"devDependencies":{"vite":"^5.0.0"}}`)
	writeFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)

	p, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Provider != "laravel" || p.Version != "8.3" || p.FrontendBuild == nil {
		t.Fatalf("unexpected Laravel full-stack plan: %#v", p)
	}
	if p.FrontendBuild.PackageManager != "npm" || p.FrontendBuild.BuildCommand != "npm run build" {
		t.Fatalf("unexpected frontend build plan: %#v", p.FrontendBuild)
	}

	dockerfile, err := p.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"FROM docker.io/library/node:22-bookworm-slim AS frontend-build",
		"RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund",
		"RUN npm run build",
		"docker-php-ext-install pdo_mysql",
		"FROM docker.io/library/php:8.3-cli-bookworm AS build",
		"apt-get install -y --no-install-recommends unzip",
		"composer install --no-dev --no-interaction --prefer-dist --no-scripts --no-autoloader",
		"RUN composer dump-autoload --no-dev --optimize",
		"COPY --from=frontend-build --chown=65532:65532 /app/public/build /app/public/build",
		"STOPSIGNAL SIGINT",
		"$filePath = realpath($publicPath . $requestPath);",
		"str_starts_with($filePath, $publicPath . DIRECTORY_SEPARATOR)",
		"require $publicPath . \"/index.php\";",
		"CMD [\"php\",\"-S\",\"0.0.0.0:8080\",\"-t\",\"public\",\".batlify-packs-router.php\"]",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Laravel Dockerfile missing %q:\n%s", required, dockerfile)
		}
	}
	runtimeStart := strings.Index(dockerfile, "FROM docker.io/library/php:8.3-cli-bookworm AS runtime")
	if runtimeStart < 0 || strings.Contains(dockerfile[runtimeStart:], "AS frontend-build") {
		t.Fatalf("runtime stage should contain the PHP runtime, not the Node builder:\n%s", dockerfile)
	}
}

func TestDetectLaravelWithoutFrontendAndRouteRequestsThroughFrontController(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "composer.json", `{"require":{"laravel/framework":"^12.0"}}`)
	writeFile(t, root, "artisan", "<?php\n")
	writeFile(t, root, "public/index.php", "<?php\n")

	p, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Provider != "laravel" || p.FrontendBuild != nil {
		t.Fatalf("unexpected backend-only Laravel plan: %#v", p)
	}
	if got, want := strings.Join(p.StartCommand, " "), "php -S 0.0.0.0:8080 -t public .batlify-packs-router.php"; got != want {
		t.Fatalf("Laravel start command = %q, want %q", got, want)
	}
}

func TestLaravelFrontendManifestMustHaveBuildScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "composer.json", `{"require":{"laravel/framework":"^12.0"}}`)
	writeFile(t, root, "artisan", "<?php\n")
	writeFile(t, root, "public/index.php", "<?php\n")
	writeFile(t, root, "package.json", `{"dependencies":{"react":"^19.0.0"}}`)
	if _, err := Detect(root); err == nil || !strings.Contains(err.Error(), "no build script") {
		t.Fatalf("expected a missing frontend build error, got %v", err)
	}
}

func TestLaravelRejectsServerRenderedNextFrontend(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "composer.json", `{"require":{"laravel/framework":"^12.0"}}`)
	writeFile(t, root, "artisan", "<?php\n")
	writeFile(t, root, "public/index.php", "<?php\n")
	writeFile(t, root, "package.json", `{"scripts":{"build":"next build"},"dependencies":{"next":"^15.0.0"}}`)
	if _, err := Detect(root); err == nil || !strings.Contains(err.Error(), "separate images") {
		t.Fatalf("expected a clear separate-image error for Laravel plus Next.js, got %v", err)
	}
}

func TestDetectDoesNotAssumeEveryPHPAndNodeRepositoryIsLaravel(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "composer.json", `{"require":{"monolog/monolog":"^3.0"}}`)
	writeFile(t, root, "artisan", "<?php\n")
	writeFile(t, root, "public/index.php", "<?php\n")
	writeFile(t, root, "package.json", `{"scripts":{"start":"node server.js"}}`)
	if _, err := Detect(root); err == nil || !strings.Contains(err.Error(), "ambiguous project") {
		t.Fatalf("expected a generic mixed project to remain ambiguous, got %v", err)
	}
}

func TestNodeRuntimeStartDoesNotRequireCorepack(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"packageManager":"pnpm@9.0.0","scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"^15.0.0"}}`)
	writeFile(t, root, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	p, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := p.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	runtimeStart := strings.Index(dockerfile, "FROM docker.io/library/node:22-bookworm-slim AS runtime")
	if runtimeStart < 0 {
		t.Fatalf("runtime stage is missing:\n%s", dockerfile)
	}
	if strings.Contains(dockerfile[runtimeStart:], "corepack") {
		t.Fatalf("runtime should invoke package scripts through npm, not require Corepack:\n%s", dockerfile)
	}
	if !strings.Contains(dockerfile[runtimeStart:], `CMD ["npm","start"]`) {
		t.Fatalf("runtime should use npm to invoke the declared start script:\n%s", dockerfile)
	}
}

func TestNextProjectMustHaveProductionDependenciesAndBuildScript(t *testing.T) {
	t.Run("next in dev dependencies", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "package.json", `{"scripts":{"build":"next build","start":"next start"},"devDependencies":{"next":"^15.0.0"}}`)
		if _, err := Detect(root); err == nil || !strings.Contains(err.Error(), "only in devDependencies") {
			t.Fatalf("expected a production dependency error, got %v", err)
		}
	})

	t.Run("missing build script", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "package.json", `{"scripts":{"start":"next start"},"dependencies":{"next":"^15.0.0"}}`)
		if _, err := Detect(root); err == nil || !strings.Contains(err.Error(), "no build script") {
			t.Fatalf("expected a missing build script error, got %v", err)
		}
	})
}

func TestDetectGoUsesScratchRuntimeAndSubdirectoryEntrypoint(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.test/app\n\ngo 1.22\n")
	writeFile(t, root, "cmd/server/main.go", "package main\nfunc main() {}\n")
	p, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.RuntimeImage != "scratch" || p.ProjectFile != "./cmd/server" {
		t.Fatalf("unexpected Go plan: %#v", p)
	}
	dockerfile, err := p.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dockerfile, `go build -trimpath -ldflags="-s -w" -o /out/app './cmd/server'`) {
		t.Fatalf("Dockerfile does not build the detected entrypoint:\n%s", dockerfile)
	}
}

func TestDetectGoRejectsMultipleCommandEntrypoints(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.test/app\n\ngo 1.22\n")
	writeFile(t, root, "cmd/api/main.go", "package main\nfunc main() {}\n")
	writeFile(t, root, "cmd/worker/main.go", "package main\nfunc main() {}\n")
	if _, err := Detect(root); err == nil || !strings.Contains(err.Error(), "cannot choose one automatically") {
		t.Fatalf("expected fail-closed multiple-entrypoint error, got %v", err)
	}
}

func TestShellQuotePreservesShellArguments(t *testing.T) {
	for input, want := range map[string]string{
		"./cmd/server": "'./cmd/server'",
		"a'b":          "'a'\\''b'",
		"$(touch x)":   "'$(touch x)'",
	} {
		if got := shellQuote(input); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDotnetRejectsUnsupportedTargetFramework(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "app.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net11.0</TargetFramework></PropertyGroup></Project>`)
	if _, err := Detect(root); err == nil || !strings.Contains(err.Error(), "unsupported .NET target framework") {
		t.Fatalf("expected an unsupported target framework error, got %v", err)
	}
}

func TestDetectFailsClosedForUnknownAndAmbiguousProjects(t *testing.T) {
	unknown := t.TempDir()
	if _, err := Detect(unknown); err == nil || !strings.Contains(err.Error(), "unable to detect") {
		t.Fatalf("expected unsupported-project error, got %v", err)
	}
	ambiguous := t.TempDir()
	writeFile(t, ambiguous, "go.mod", "module example.test/app\n")
	writeFile(t, ambiguous, "package.json", `{"scripts":{"start":"node index.js"}}`)
	if _, err := Detect(ambiguous); err == nil || !strings.Contains(err.Error(), "ambiguous project") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}

	devRequirementsOnly := t.TempDir()
	writeFile(t, devRequirementsOnly, "requirements-dev.txt", "pytest\n")
	if _, err := Detect(devRequirementsOnly); err == nil || !strings.Contains(err.Error(), "unable to detect") {
		t.Fatalf("development-only requirements should not identify a deployable app, got %v", err)
	}
}

func TestDetectPythonPEP621InstallsAfterCopy(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pyproject.toml", "[project]\nname='demo'\ndependencies=[]\n")
	writeFile(t, root, "main.py", "print('ready')\n")
	p, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !p.InstallAfterCopy {
		t.Fatal("PEP 621 local project install should happen after source copy")
	}
	dockerfile, err := p.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(dockerfile, "COPY . .") > strings.Index(dockerfile, "pip install") {
		t.Fatalf("Python project install ran before source was copied:\n%s", dockerfile)
	}
	if strings.Contains(dockerfile, "COPY --from=build /usr/local /usr/local") {
		t.Fatalf("Python runtime should not duplicate its base interpreter:\n%s", dockerfile)
	}
}

func TestPythonRequirementsUseSmallRuntimeCopyAndInstallLocalPackagesAfterCopy(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "requirements.txt", "-e ./packages/shared\nfastapi==0.115.0\n")
	writeFile(t, root, "main.py", "print('ready')\n")
	p, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if !p.InstallAfterCopy {
		t.Fatal("local requirements should install after source is copied")
	}
	if !strings.Contains(p.InstallCommand, "--prefix=/install") {
		t.Fatalf("Python dependencies should be isolated from the runtime base: %q", p.InstallCommand)
	}
	dockerfile, err := p.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dockerfile, "COPY --from=build /install /install") || !strings.Contains(dockerfile, "PYTHONPATH=/install/lib/python3.12/site-packages") {
		t.Fatalf("Python runtime should copy only installed packages:\n%s", dockerfile)
	}
}

func TestDetectNodeStaticFrontendUsesOnlyNginxRuntime(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"scripts":{"build":"vite build"},"devDependencies":{"vite":"^5.0.0"}}`)
	writeFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	writeFile(t, root, "index.html", "<div id=app></div>")
	p, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.RuntimeKind != "node-static" || p.RuntimeImage != "nginxinc/nginx-unprivileged:alpine" || p.RuntimeOutputDir != "dist" {
		t.Fatalf("unexpected frontend plan: %#v", p)
	}
	dockerfile, err := p.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dockerfile, "RUN npm run build") || !strings.Contains(dockerfile, `COPY --chown=nginx:nginx --from=build ["/app/dist/", "/usr/share/nginx/html/"]`) {
		t.Fatalf("static image does not contain a build stage and static runtime:\n%s", dockerfile)
	}
	if strings.Contains(dockerfile, "FROM node:22-bookworm-slim AS runtime") {
		t.Fatalf("Node runtime should not be present in a static image:\n%s", dockerfile)
	}
}

func TestDetectDenoAndCMakeEntrypoints(t *testing.T) {
	t.Run("deno", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "deno.json", `{"tasks":{"start":"deno run --allow-net main.ts"}}`)
		writeFile(t, root, "main.ts", `Deno.serve(() => new Response("ok"));`)
		p, err := Detect(root)
		if err != nil {
			t.Fatal(err)
		}
		if p.RuntimeKind != "deno" || p.ProjectFile != "main.ts" {
			t.Fatalf("unexpected Deno plan: %#v", p)
		}
	})
	t.Run("cmake", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "CMakeLists.txt", "cmake_minimum_required(VERSION 3.20)\nproject(demo)\nadd_executable(demo main.cpp)\n")
		writeFile(t, root, "main.cpp", "int main() { return 0; }\n")
		p, err := Detect(root)
		if err != nil {
			t.Fatal(err)
		}
		if p.RuntimeKind != "cmake" || p.ProjectFile != "demo" || p.RuntimeImage != "scratch" {
			t.Fatalf("unexpected CMake plan: %#v", p)
		}
	})
}

func TestDetectPoetryUsesVenvAndKeepsBuilderToolsOutOfRuntime(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pyproject.toml", "[tool.poetry]\nname='demo'\nversion='0.1.0'\n")
	writeFile(t, root, "poetry.lock", "# lock\n")
	writeFile(t, root, "main.py", "print('ready')\n")
	p, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.PackageManager != "Poetry" || !p.RuntimeVenv {
		t.Fatalf("unexpected Poetry plan: %#v", p)
	}
	dockerfile, err := p.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dockerfile, "COPY --from=build --chown=65532:65532 /app /app") || strings.Contains(dockerfile, "COPY --from=build /usr/local /usr/local") {
		t.Fatalf("Poetry tooling would leak into the runtime image:\n%s", dockerfile)
	}
}

func TestGradleDockerfilePreservesBuildStageOrdering(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "build.gradle", "plugins { id 'java' }\n")
	writeFile(t, root, "settings.gradle", "rootProject.name = 'demo'\n")

	p, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := p.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	ordered := []string{
		"FROM docker.io/library/gradle:8-jdk21 AS build",
		"USER root",
		"ENV GRADLE_USER_HOME=/root/.gradle",
		"WORKDIR /src",
		"COPY . .",
		"RUN --mount=type=cache,target=/root/.gradle gradle --no-daemon build -x test",
		"FROM docker.io/library/eclipse-temurin:21-jre-jammy AS runtime",
	}
	position := 0
	for _, fragment := range ordered {
		relative := strings.Index(dockerfile[position:], fragment)
		if relative < 0 {
			t.Fatalf("Dockerfile is missing %q or places it out of order:\n%s", fragment, dockerfile)
		}
		position += relative + len(fragment)
	}
}

func TestGeneratedDockerfileUsesFullyQualifiedBaseImages(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.html", "hello")
	p, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := p.Dockerfile()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dockerfile, "FROM docker.io/nginxinc/nginx-unprivileged:alpine AS runtime") {
		t.Fatalf("image should not rely on the builder's short-name registry config:\n%s", dockerfile)
	}
}

func TestScratchRemainsASpecialBaseImage(t *testing.T) {
	if got := qualifyImageReference("scratch"); got != "scratch" {
		t.Fatalf("scratch image was rewritten to %q", got)
	}
}
