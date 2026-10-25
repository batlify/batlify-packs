package plan

import (
	"fmt"
	"strings"
)

func writeCMakeDockerfile(b *strings.Builder, p Plan) {
	b.WriteString(`FROM debian:bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends build-essential cmake && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY . .
`)
	target := dockerPath(p.ProjectFile)
	if target == "" {
		target = "app"
	}
	fmt.Fprintf(b, "RUN %s && artifact=$(find /tmp/build -type f -name %s -executable | head -n 1) && test -n \"$artifact\" && install -D \"$artifact\" /out/app\n", p.BuildCommand, shellQuote(target))
	b.WriteString(`
FROM scratch AS runtime
COPY --from=build /out/app /app
USER 65532:65532
`)
	writeExposeAndCommand(b, p)
}

func writeGoDockerfile(b *strings.Builder, p Plan) {
	fmt.Fprintf(b, "FROM %s AS build\nWORKDIR /src\n", p.BuilderImage)
	for _, f := range p.DependencyFiles {
		writeManifestCopy(b, f)
	}
	b.WriteString("RUN --mount=type=cache,target=/go/pkg/mod go mod download\nCOPY . .\n")
	target := p.ProjectFile
	if target == "" {
		target = "."
	}
	fmt.Fprintf(b, "RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags=\"-s -w\" -o /out/app %s\n\n", shellQuote(target))
	b.WriteString(`FROM scratch AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/app /app
USER 65532:65532
`)
	writeExposeAndCommand(b, p)
}

func writeRustDockerfile(b *strings.Builder, p Plan) {
	fmt.Fprintf(b, "FROM %s AS build\nWORKDIR /src\n", p.BuilderImage)
	for _, f := range p.DependencyFiles {
		writeManifestCopy(b, f)
	}
	name := dockerPath(strings.TrimPrefix(p.ProjectFile, "Cargo.toml:"))
	if name == "" {
		name = "app"
	}
	fmt.Fprintf(b, `COPY . .
RUN --mount=type=cache,target=/usr/local/cargo/registry --mount=type=cache,target=/usr/local/cargo/git --mount=type=cache,target=/src/target cargo build --release && install -D %s /out/app

FROM %s AS runtime
WORKDIR /app
COPY --from=build /out/app /usr/local/bin/app
USER 65532:65532
`, shellQuote("/src/target/release/"+name), p.RuntimeImage)
	writeExposeAndCommand(b, p)
}

func writeJavaDockerfile(b *strings.Builder, p Plan) {
	var environment []string
	if p.PackageManager == "Gradle" {
		environment = []string{"GRADLE_USER_HOME=/root/.gradle"}
	}
	writeBuildStage(b, buildStageSpec{
		image:            p.BuilderImage,
		name:             "build",
		user:             "root",
		workdir:          "/src",
		environment:      environment,
		dependencyFiles:  p.DependencyFiles,
		installCommand:   p.InstallCommand,
		installCache:     "/root/.m2",
		installAfterCopy: p.InstallAfterCopy,
		buildCommand:     p.BuildCommand,
		buildCache:       p.BuildCacheTarget,
	})
	fmt.Fprintf(b, "\nFROM %s AS runtime\nWORKDIR /app\nCOPY --from=build /out/app.jar /app/app.jar\nUSER 65532:65532\n", p.RuntimeImage)
	writeExposeAndCommand(b, p)
}

func writeDotnetDockerfile(b *strings.Builder, p Plan) {
	project := dockerPath(p.ProjectFile)
	quotedProject := dockerJSON(project)
	fmt.Fprintf(b, `FROM %s AS build
WORKDIR /src
COPY [%s, %s]
RUN --mount=type=cache,target=/root/.nuget/packages dotnet restore %s
COPY . .
RUN --mount=type=cache,target=/root/.nuget/packages dotnet publish %s -c Release --no-restore -o /out /p:UseAppHost=false

FROM %s AS runtime
WORKDIR /app
COPY --from=build /out/ .
`, p.BuilderImage, quotedProject, quotedProject, shellQuote(project), shellQuote(project), p.RuntimeImage)
	if p.Port != 0 && p.Port == 8080 {
		b.WriteString("ENV ASPNETCORE_URLS=http://+:8080\n")
	}
	b.WriteString("USER 65532:65532\n")
	writeExposeAndCommand(b, p)
}

func writeRubyDockerfile(b *strings.Builder, p Plan) {
	writeBuildStage(b, buildStageSpec{
		image:           p.BuilderImage,
		name:            "build",
		workdir:         "/app",
		dependencyFiles: p.DependencyFiles,
		installCommand:  p.InstallCommand,
		installCache:    "/usr/local/bundle/cache",
	})
	fmt.Fprintf(b, "\nFROM %s AS runtime\nWORKDIR /app\nENV BUNDLE_WITHOUT=development:test\nCOPY --from=build --chown=65532:65532 /usr/local/bundle /usr/local/bundle\nCOPY --from=build --chown=65532:65532 /app /app\nUSER 65532:65532\n", p.RuntimeImage)
	writeExposeAndCommand(b, p)
}

func writeElixirDockerfile(b *strings.Builder, p Plan) {
	fmt.Fprintf(b, "FROM %s AS build\nWORKDIR /app\n", p.BuilderImage)
	for _, f := range p.DependencyFiles {
		writeManifestCopy(b, f)
	}
	fmt.Fprintf(b, `ENV MIX_ENV=prod
RUN mix local.hex --force && mix local.rebar --force
RUN --mount=type=cache,target=/root/.hex %s
COPY . .
RUN %s

FROM %s AS runtime
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libncurses6 libssl3 libstdc++6 && rm -rf /var/lib/apt/lists/*
COPY --from=build --chown=65532:65532 /app/_build/prod/rel/%s /app
USER 65532:65532
`, p.InstallCommand, p.BuildCommand, p.RuntimeImage, dockerPath(p.ProjectFile))
	writeExposeAndCommand(b, p)
}
