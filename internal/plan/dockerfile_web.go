package plan

import (
	"fmt"
	"strings"
)

func writeNodeDockerfile(b *strings.Builder, p Plan) {
	writeJavaScriptBuildStage(b, p, "build", nodeCacheTarget(p.PackageManager))
	if p.ProductionCommand != "" {
		fmt.Fprintf(b, "RUN %s\n", p.ProductionCommand)
	}
	if p.NextStandalone {
		fmt.Fprintf(b, "\nFROM %s AS runtime\nWORKDIR /app\nENV NODE_ENV=production HOSTNAME=0.0.0.0 PORT=%d\nCOPY --from=build --chown=node:node /app/.next/standalone/ /app/\nUSER node\n", p.RuntimeImage, p.Port)
		writeExposeAndCommand(b, p)
		return
	}
	fmt.Fprintf(b, "\nFROM %s AS runtime\nWORKDIR /app\nENV NODE_ENV=production\nCOPY --from=build --chown=node:node /app /app\nUSER node\n", p.RuntimeImage)
	writeExposeAndCommand(b, p)
}

func writeBunDockerfile(b *strings.Builder, p Plan) {
	writeJavaScriptBuildStage(b, p, "build", "/root/.bun/install/cache")
	if p.ProductionCommand != "" {
		fmt.Fprintf(b, "RUN %s\n", p.ProductionCommand)
	}
	fmt.Fprintf(b, `
FROM %s AS runtime
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build --chown=65532:65532 /app /app
USER 65532:65532
`, p.RuntimeImage)
	writeExposeAndCommand(b, p)
}

func writeJavaScriptBuildStage(b *strings.Builder, p Plan, stage, cacheTarget string) {
	writeBuildStage(b, buildStageSpec{
		image:            p.BuilderImage,
		name:             stage,
		workdir:          "/app",
		dependencyFiles:  p.DependencyFiles,
		installCommand:   p.InstallCommand,
		installCache:     cacheTarget,
		installAfterCopy: p.InstallAfterCopy,
		buildCommand:     p.BuildCommand,
	})
}

func writeDenoDockerfile(b *strings.Builder, p Plan) {
	fmt.Fprintf(b, "FROM %s AS build\nWORKDIR /app\nENV DENO_DIR=/deno-dir\n", p.BuilderImage)
	for _, f := range p.DependencyFiles {
		writeManifestCopy(b, f)
	}
	b.WriteString("COPY . .\n")
	if p.ProjectFile != "" {
		fmt.Fprintf(b, "RUN deno cache %s\n", shellQuote(dockerPath(p.ProjectFile)))
	}
	if p.BuildCommand != "" {
		fmt.Fprintf(b, "RUN %s\n", p.BuildCommand)
	}
	fmt.Fprintf(b, `
FROM %s AS runtime
WORKDIR /app
ENV DENO_DIR=/deno-dir
COPY --from=build /deno-dir /deno-dir
COPY --from=build --chown=deno:deno /app /app
USER deno
`, p.RuntimeImage)
	writeExposeAndCommand(b, p)
}

func writeNodeStaticDockerfile(b *strings.Builder, p Plan) {
	writeJavaScriptBuildStage(b, p, "build", nodeCacheTarget(p.PackageManager))
	output := dockerPath(p.RuntimeOutputDir)
	if output == "" {
		output = "dist"
	}
	copySource := dockerJSON("/app/" + output + "/")
	copyTarget := dockerJSON("/usr/share/nginx/html/")
	fmt.Fprintf(b, `
FROM nginxinc/nginx-unprivileged:alpine AS runtime
COPY --chown=nginx:nginx --from=build [%s, %s]
EXPOSE 8080
USER nginx
`, copySource, copyTarget)
	writeJSONCommand(b, []string{"nginx", "-g", "daemon off;"})
}

func writePythonDockerfile(b *strings.Builder, p Plan) {
	writeBuildStage(b, buildStageSpec{
		image:            p.BuilderImage,
		name:             "build",
		workdir:          "/app",
		dependencyFiles:  p.DependencyFiles,
		installCommand:   p.InstallCommand,
		installCache:     "/root/.cache/pip",
		installAfterCopy: p.InstallAfterCopy,
		buildCommand:     p.BuildCommand,
	})
	fmt.Fprintf(b, "\nFROM %s AS runtime\nWORKDIR /app\nENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1\n", p.RuntimeImage)
	if p.RuntimeVenv {
		b.WriteString("ENV PATH=/app/.venv/bin:$PATH\nCOPY --from=build --chown=65532:65532 /app /app\n")
	} else {
		if p.InstallCommand != "" {
			fmt.Fprintf(b, "ENV PATH=/install/bin:$PATH PYTHONPATH=/install/lib/python%s/site-packages\n", p.Version)
			b.WriteString("COPY --from=build /install /install\n")
		}
		b.WriteString("COPY --from=build --chown=65532:65532 /app /app\n")
	}
	b.WriteString("USER 65532:65532\n")
	writeExposeAndCommand(b, p)
}

func writePHPDockerfile(b *strings.Builder, p Plan) {
	if p.InstallCommand != "" {
		b.WriteString("FROM composer:2 AS composer-bin\n")
	}
	fmt.Fprintf(b, "FROM %s AS build\n", p.BuilderImage)
	if p.InstallCommand != "" {
		b.WriteString("COPY --from=composer-bin /usr/bin/composer /usr/bin/composer\n")
	}
	b.WriteString("WORKDIR /app\n")
	for _, f := range p.DependencyFiles {
		writeManifestCopy(b, f)
	}
	if p.InstallCommand != "" {
		fmt.Fprintf(b, "RUN --mount=type=cache,target=/root/.cache/composer %s\n", p.InstallCommand)
	}
	fmt.Fprintf(b, `COPY . .

FROM %s AS runtime
WORKDIR /app
COPY --from=build --chown=65532:65532 /app /app
USER 65532:65532
STOPSIGNAL SIGINT
`, p.RuntimeImage)
	writeExposeAndCommand(b, p)
}

func writeLaravelDockerfile(b *strings.Builder, p Plan) {
	if p.FrontendBuild != nil {
		frontend := p.FrontendBuild
		writeBuildStage(b, buildStageSpec{
			image:            frontend.BuilderImage,
			name:             "frontend-build",
			workdir:          "/app",
			dependencyFiles:  frontend.DependencyFiles,
			installCommand:   frontend.InstallCommand,
			installCache:     frontend.CacheTarget,
			installAfterCopy: frontend.InstallAfterCopy,
			buildCommand:     frontend.BuildCommand,
		})
	}
	fmt.Fprintf(b, "FROM %s AS php-extensions\n", p.BuilderImage)
	b.WriteString(`RUN docker-php-ext-install pdo_mysql \
    && ext_dir="$(php-config --extension-dir)" \
    && install -D "$ext_dir/pdo_mysql.so" "/out$ext_dir/pdo_mysql.so" \
    && install -D /usr/local/etc/php/conf.d/docker-php-ext-pdo_mysql.ini /out/usr/local/etc/php/conf.d/docker-php-ext-pdo_mysql.ini

`)
	b.WriteString("FROM composer:2 AS composer-bin\n")
	fmt.Fprintf(b, "FROM %s AS build\n", p.BuilderImage)
	b.WriteString("COPY --from=php-extensions /out/ /\n")
	b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends unzip && rm -rf /var/lib/apt/lists/*\n")
	b.WriteString("COPY --from=composer-bin /usr/bin/composer /usr/bin/composer\nWORKDIR /app\n")
	for _, file := range p.DependencyFiles {
		writeManifestCopy(b, file)
	}
	fmt.Fprintf(b, "RUN --mount=type=cache,target=/root/.cache/composer %s\n", p.InstallCommand)
	b.WriteString("COPY . .\n")
	fmt.Fprintf(b, "RUN %s\n", p.BuildCommand)

	fmt.Fprintf(b, "\nFROM %s AS runtime\nWORKDIR /app\n", p.RuntimeImage)
	b.WriteString("COPY --from=php-extensions /out/ /\n")
	b.WriteString("COPY --from=build --chown=65532:65532 /app /app\n")
	if p.FrontendBuild != nil {
		b.WriteString("COPY --from=frontend-build --chown=65532:65532 /app/public/build /app/public/build\n")
	}
	b.WriteString("RUN mkdir -p storage/framework/cache/data storage/framework/sessions storage/framework/views storage/logs bootstrap/cache && chown -R 65532:65532 storage bootstrap/cache\n")
	b.WriteString("RUN printf '%s\\n' '<?php' '$publicPath = realpath(__DIR__ . \"/public\");' '$requestPath = parse_url($_SERVER[\"REQUEST_URI\"], PHP_URL_PATH) ?? \"/\";' '$filePath = realpath($publicPath . $requestPath);' 'if ($requestPath !== \"/\" && $filePath !== false && str_starts_with($filePath, $publicPath . DIRECTORY_SEPARATOR) && is_file($filePath)) {' '    return false;' '}' 'require $publicPath . \"/index.php\";' > /app/.batlify-packs-router.php\n")
	b.WriteString("USER 65532:65532\n")
	b.WriteString("STOPSIGNAL SIGINT\n")
	writeExposeAndCommand(b, p)
}

func writeStaticDockerfile(b *strings.Builder) {
	b.WriteString(`FROM nginxinc/nginx-unprivileged:alpine AS runtime
COPY --chown=nginx:nginx . /usr/share/nginx/html
EXPOSE 8080
USER nginx
`)
	writeJSONCommand(b, []string{"nginx", "-g", "daemon off;"})
}
