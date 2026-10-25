# batlify-packs

**Build container images from application source without maintaining a Dockerfile.**

`batlify-packs` detects a supported project, creates a build plan, and builds an image with Docker BuildKit. It aims to keep builds cacheable and runtime images small by separating build tools from runtime files where the project type allows it.

> This project is an early release. Provider support is explicit and convention-based; it does not cover every language, framework, or repository layout.

## Requirements

- Go 1.22 or later to build the CLI from source.
- Docker CLI connected to a running Docker Engine.
- Docker BuildKit support for cache mounts and the generated Dockerfile syntax.

The build runs dependency installation and project build scripts inside the container build. Only build projects whose source and dependencies you trust.

## Build the CLI

From the repository root:

```sh
go build -buildvcs=false -o batlify-packs .
```

The `-buildvcs=false` option also works in source archives that do not include Git metadata. The resulting executable is `./batlify-packs`.

## Quick start

Build an image for a project in the current directory:

```sh
./batlify-packs plan .
./batlify-packs build . --name my-app:latest
```

`plan` is optional. It prints the detected provider, image bases, package manager, start command, and port without building an image. `build` detects the project itself, so it can be run directly:

```sh
./batlify-packs build /path/to/project --name my-app:latest
```

Build without reusing Docker build layers:

```sh
./batlify-packs build /path/to/project --name my-app:latest --no-cache
```

Run the image using the port reported by `plan`:

```sh
docker run --rm -p 3000:3000 my-app:latest
```

Change the host and container port to match your application. Runtime environment variables can be passed with Docker's `--env` or `--env-file` options.

## Next.js example

From the `batlify-packs` repository, build the CLI and then point it at the Next.js project:

```sh
go build -buildvcs=false -o batlify-packs .
./batlify-packs build /path/to/nextjs-app --name my-next-app:dev
docker run --rm -p 3000:3000 my-next-app:dev
```

The project must declare a `build` script and either a usable production `start` script or a Next.js dependency in `dependencies`. The CLI does not currently expose build-time environment variables; projects that require them need a recipe change before they can be built. Runtime variables can be passed to `docker run`.

## Built-in providers

Detection uses project manifests and conventional entry points. A provider may reject a project when it cannot safely infer how to build or start it.

| Project type | Detection and current conventions |
| --- | --- |
| Node.js | `package.json`; npm, pnpm, Yarn, or Bun; server apps and common static output from Vite, Create React App, Gatsby, Angular, Vue CLI, and Astro. Next.js uses its production start command. |
| Deno | `deno.json` or `deno.jsonc` with a `start` task, or a conventional entry file. |
| Python | `requirements.txt`, `requirements-prod.txt`, `pyproject.toml`, `Pipfile`, or `setup.py`; conventional Django, Flask, FastAPI, or Python entry points. |
| Go | `go.mod` and a root `main.go`, or one command under `cmd/`. |
| Rust | A root `Cargo.toml` with one package. |
| Java and JVM | Maven or Gradle projects that produce an executable JAR. |
| .NET | One `.csproj`, `.fsproj`, or `.vbproj` file at the project root. |
| Ruby | Bundler projects with a Rack, Rails, or `app.rb` entry point. |
| Laravel | A root `composer.json` requiring `laravel/framework`, plus `artisan` and `public/index.php`. If a root `package.json` is present, it must define a `build` script that writes assets under `public/build` (the Laravel Vite default). A server-rendered Next.js app must be built as a separate image. |
| PHP | Composer projects or a conventional `index.php` entry point. |
| Elixir | Mix releases with a lockfile and an inferable application name. |
| C and C++ | CMake projects with a literal `add_executable` target. |
| Static sites | A root `index.html`. |

If no provider matches, or multiple providers match, the build stops with an error. A `Dockerfile` in the project is not used as a fallback; `batlify-packs` builds from a supported source layout using its generated recipe.

## Build behavior

- Generates a multi-stage Dockerfile internally and streams it with the filtered build context. It does not write a generated Dockerfile into the project.
- Uses BuildKit cache mounts for supported package managers and compilers.
- Uses separate runtime stages where practical; Go and eligible CMake outputs use `scratch` runtimes.
- Excludes common source-control metadata, environment files, dependency caches, and build output directories by default. Project `.dockerignore` rules are also applied.
- Uses non-root runtime users where the selected base image supports them.

Base image tags follow provider defaults and are not pinned by digest. Use lockfiles where available for repeatable dependency resolution.

Laravel builds install production Composer dependencies and run Composer's optimized autoloader scripts after copying the application source. A root JavaScript manifest is built in a separate Node.js stage, and only `public/build` is copied into the final PHP image. The runtime includes PDO MySQL and SQLite support. A generated PHP router serves existing static files and forwards application requests to Laravel's front controller on port `8080`. Provide values such as `APP_KEY` and database settings at runtime with Docker environment options. This default server is intended for straightforward container use and is not tuned as a high-concurrency PHP-FPM deployment. Other PHP extensions are not inferred automatically; Composer stops the build if a declared extension requirement is missing.

## Development

```sh
go fmt ./...
go test ./...
go test -race ./...
go vet ./...
```

Provider changes should include detection and generated-recipe coverage. Container integration checks should also verify that the produced image starts and serves the expected application.

## License

No license has been selected for this repository yet. Add a `LICENSE` file before publishing so users know how they may use and redistribute the project.
