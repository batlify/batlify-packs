package plan

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// JavaScriptBuild describes a frontend build embedded in another application.
type JavaScriptBuild struct {
	Version           string   `json:"version"`
	BuilderImage      string   `json:"builderImage"`
	PackageManager    string   `json:"packageManager"`
	DependencyFiles   []string `json:"dependencyFiles,omitempty"`
	InstallCommand    string   `json:"installCommand"`
	BuildCommand      string   `json:"buildCommand"`
	InstallAfterCopy  bool     `json:"installAfterCopy,omitempty"`
	CacheTarget       string   `json:"-"`
	ProductionCommand string   `json:"-"`
}

func detectJavaScriptBuild(root string, pkg nodeManifest) JavaScriptBuild {
	nvmrc := read(root, ".nvmrc")
	version := firstMatch(regexp.MustCompile(`(?m)^\s*v?([0-9]+(?:\.[0-9]+){2})\s*$`), nvmrc, "")
	if version == "" {
		version = firstMatch(regexp.MustCompile(`(?m)^\s*v?([0-9]+)`), nvmrc, "")
	}
	if version == "" {
		version = firstMatch(regexp.MustCompile(`(?:^|[^0-9])([0-9]+)`), pkg.Engines["node"], "22")
	}

	build := JavaScriptBuild{
		Version:           version,
		PackageManager:    "npm",
		DependencyFiles:   filesMatching(root, "package.json", "package-lock.json", "npm-shrinkwrap.json"),
		InstallCommand:    "npm install --no-audit --no-fund",
		ProductionCommand: "npm prune --omit=dev --no-audit --no-fund && npm cache clean --force",
		InstallAfterCopy:  installAfterCopyCandidate(root, pkg),
	}
	if exists(root, "package-lock.json") || exists(root, "npm-shrinkwrap.json") {
		build.InstallCommand = "npm ci --no-audit --no-fund"
	}
	if strings.HasPrefix(pkg.PackageMgr, "bun@") || anyExists(root, "bun.lock", "bun.lockb") {
		build.PackageManager = "bun"
		build.Version = firstMatch(regexp.MustCompile(`bun@(?:v)?([0-9]+\.[0-9]+)`), pkg.PackageMgr, "1")
		build.InstallCommand = "bun install --frozen-lockfile"
		build.ProductionCommand = "bun install --production --frozen-lockfile"
		build.DependencyFiles = filesMatching(root, "package.json", "bun.lock", "bun.lockb")
	} else if strings.HasPrefix(pkg.PackageMgr, "pnpm@") || exists(root, "pnpm-lock.yaml") {
		build.PackageManager = "pnpm"
		build.InstallCommand = "corepack enable && corepack pnpm install --frozen-lockfile"
		build.ProductionCommand = "corepack pnpm prune --prod"
		if build.InstallAfterCopy {
			build.ProductionCommand = "corepack pnpm install --prod --frozen-lockfile"
		}
		build.DependencyFiles = filesMatching(root, "package.json", "pnpm-lock.yaml", "pnpm-workspace.yaml")
	} else if strings.HasPrefix(pkg.PackageMgr, "yarn@") || exists(root, "yarn.lock") {
		build.PackageManager = "yarn"
		if strings.Contains(read(root, "yarn.lock"), "__metadata:") {
			build.InstallCommand = "corepack enable && corepack yarn install --immutable"
			build.ProductionCommand = "corepack yarn plugin import workspace-tools && corepack yarn workspaces focus --production && rm -rf .yarn/plugins"
		} else {
			build.InstallCommand = "corepack enable && corepack yarn install --frozen-lockfile"
			build.ProductionCommand = "corepack yarn install --production --frozen-lockfile"
		}
		build.DependencyFiles = filesMatching(root, "package.json", "yarn.lock", ".yarnrc.yml")
	}
	if _, ok := pkg.Scripts["build"]; ok {
		build.BuildCommand = "npm run build"
		switch build.PackageManager {
		case "pnpm":
			build.BuildCommand = "corepack pnpm run build"
		case "yarn":
			build.BuildCommand = "corepack yarn build"
		case "bun":
			build.BuildCommand = "bun run build"
		}
	}
	majorVersion := firstMatch(regexp.MustCompile(`^([0-9]+)`), build.Version, "22")
	if major, err := strconv.Atoi(majorVersion); err == nil && major < 18 && build.PackageManager != "bun" {
		build.BuilderImage = ""
		return build
	}
	build.BuilderImage = "node:" + build.Version + "-bookworm-slim"
	if build.PackageManager == "bun" {
		build.BuilderImage = "oven/bun:1"
	}
	build.CacheTarget = nodeCacheTarget(build.PackageManager)
	return build
}

func requireSupportedJavaScriptBuild(build JavaScriptBuild) error {
	if build.BuilderImage == "" {
		return fmt.Errorf("Node.js %s is no longer supported by the provider", build.Version)
	}
	return nil
}
