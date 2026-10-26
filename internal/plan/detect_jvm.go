package plan

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

func javaPlan(root string) (Plan, error) {
	gradle := exists(root, "build.gradle") || exists(root, "build.gradle.kts")
	sbt := exists(root, "build.sbt")
	if sbt && (gradle || exists(root, "pom.xml")) {
		return Plan{}, errors.New("project contains multiple JVM build systems; keep one root build file or split the projects")
	}
	if sbt {
		return scalaPlan(root)
	}
	version := "21"
	command, image, files := "mvn -B -DskipTests package && "+selectJarCommand("target"), "maven:3.9-eclipse-temurin-21", filesMatching(root, "pom.xml")
	cacheTarget := "/root/.m2"
	if gradle {
		image = "gradle:8-jdk21"
		cacheTarget = "/root/.gradle"
		command = "gradle --no-daemon build -x test && " + selectJarCommand("build/libs")
		files = filesMatching(root, "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "gradle.properties", "gradlew", "gradle/wrapper/*")
		if exists(root, "gradlew") {
			command = "chmod +x gradlew && ./gradlew --no-daemon build -x test && " + selectJarCommand("build/libs")
		}
	} else {
		files = filesMatching(root, "pom.xml", "mvnw", ".mvn/wrapper/*")
		if exists(root, "mvnw") {
			image = "eclipse-temurin:21-jdk-jammy"
			command = "chmod +x mvnw && ./mvnw -B -DskipTests package && " + selectJarCommand("target")
		}
	}
	version = firstMatch(
		regexp.MustCompile(`(?is)(?:maven.compiler.(?:release|target)|<release>|<java.version>)[^0-9]*([0-9]{1,2})`),
		strings.Join([]string{read(root, "pom.xml"), read(root, "build.gradle"), read(root, "build.gradle.kts")}, "\n"),
		"21",
	)
	runtime := "eclipse-temurin:" + version + "-jre-jammy"
	if !anyExists(root, "pom.xml", "build.gradle", "build.gradle.kts") {
		return Plan{}, errors.New("Java project has no Maven or Gradle build file")
	}
	packageManager := "Maven"
	if gradle {
		packageManager = "Gradle"
	}
	return Plan{
		Provider:         "java",
		Language:         "Java/JVM",
		Version:          version,
		BuilderImage:     image,
		RuntimeImage:     runtime,
		RuntimeKind:      "java",
		PackageManager:   packageManager,
		BuildCommand:     command,
		BuildCacheTarget: cacheTarget,
		StartCommand:     []string{"java", "-jar", "/app/app.jar"},
		Port:             8080,
		DependencyFiles:  files,
	}, nil
}

func scalaPlan(root string) (Plan, error) {
	buildProperties := read(root, "project/build.properties")
	sbtVersion := firstMatch(regexp.MustCompile(`(?m)^\s*sbt\.version\s*=\s*([0-9]+)\.[0-9]+`), buildProperties, "")
	if sbtVersion == "" {
		return Plan{}, errors.New("Scala/SBT project needs a pinned sbt.version in project/build.properties")
	}
	if sbtVersion != "1" && sbtVersion != "2" {
		return Plan{}, fmt.Errorf("unsupported SBT major version %s; the provider supports SBT 1.x and 2.x", sbtVersion)
	}
	plugins := strings.Join([]string{read(root, "build.sbt"), read(root, "project/plugins.sbt")}, "\n")
	if !strings.Contains(plugins, "sbt-assembly") {
		return Plan{}, errors.New("Scala/SBT project needs the sbt-assembly plugin to produce a self-contained runtime JAR")
	}
	return Plan{
		Provider:         "scala",
		Language:         "Scala",
		Version:          "21",
		BuilderImage:     "sbtscala/scala-sbt:eclipse-temurin-21_" + sbtVersion + ".x",
		RuntimeImage:     "eclipse-temurin:21-jre-jammy",
		RuntimeKind:      "java",
		PackageManager:   "SBT",
		BuildCommand:     "sbt -batch assembly && " + selectScalaAssemblyCommand(),
		BuildCacheTarget: "/home/sbtuser/.cache/coursier",
		StartCommand:     []string{"java", "-jar", "/app/app.jar"},
		Port:             8080,
		DependencyFiles:  filesMatching(root, "build.sbt", "project/build.properties", "project/plugins.sbt", "project/*.scala", "project/*.sbt"),
	}, nil
}

func selectScalaAssemblyCommand() string {
	return strings.Join([]string{
		`artifact=$(find target -type f -path 'target/scala-*/*.jar' -name '*assembly*.jar' ! -name '*-sources.jar' ! -name '*-javadoc.jar' | sort | head -n 1)`,
		`test -n "$artifact"`,
		`mkdir -p /out`,
		`cp "$artifact" /out/app.jar`,
	}, " && \\\n     ")
}

func selectJarCommand(outputDir string) string {
	return strings.Join([]string{
		fmt.Sprintf(`artifact=$(find %s -maxdepth 1 -type f -name '*.jar' ! -name '*-sources.jar' ! -name '*-javadoc.jar' ! -name 'original-*' ! -name '*-plain.jar' | sort | head -n 1)`, outputDir),
		fmt.Sprintf(`if [ -z "$artifact" ]; then artifact=$(find %s -maxdepth 1 -type f -name '*.jar' ! -name '*-sources.jar' ! -name '*-javadoc.jar' ! -name 'original-*' | sort | head -n 1); fi`, outputDir),
		`test -n "$artifact"`,
		`mkdir -p /out`,
		`cp "$artifact" /out/app.jar`,
	}, " && \\\n    ")
}

func dotnetPlan(root string) (Plan, error) {
	projects := filesMatching(root, "*.csproj", "*.fsproj", "*.vbproj")
	if len(projects) != 1 {
		return Plan{}, fmt.Errorf("expected exactly one .NET project file at the project root; found %d", len(projects))
	}
	content := read(root, projects[0])
	version := firstMatch(regexp.MustCompile(`(?i)<TargetFrameworks?>[^<]*net([0-9]+)\.0`), content, "8")
	switch version {
	case "6", "7", "8", "9", "10":
	default:
		return Plan{}, fmt.Errorf("unsupported .NET target framework net%s.0; the provider supports .NET 6 through 10", version)
	}
	web := strings.Contains(content, "Microsoft.NET.Sdk.Web")
	runtime := "mcr.microsoft.com/dotnet/runtime:" + version + ".0"
	port := 0
	if web {
		runtime = "mcr.microsoft.com/dotnet/aspnet:" + version + ".0"
		port = 8080
	}
	defaultAssemblyName := strings.TrimSuffix(projects[0], filepath.Ext(projects[0]))
	assemblyName := firstMatch(
		regexp.MustCompile(`(?i)<AssemblyName>\s*([^<]+)\s*</AssemblyName>`),
		content,
		defaultAssemblyName,
	)
	assembly := strings.TrimSpace(assemblyName) + ".dll"
	return Plan{
		Provider:       "dotnet",
		Language:       ".NET",
		Version:        version,
		BuilderImage:   "mcr.microsoft.com/dotnet/sdk:" + version + ".0",
		RuntimeImage:   runtime,
		RuntimeKind:    "dotnet",
		PackageManager: "dotnet",
		StartCommand:   []string{"dotnet", assembly},
		Port:           port,
		ProjectFile:    projects[0],
	}, nil
}
