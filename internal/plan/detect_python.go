package plan

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

func pythonPlan(root string) (Plan, error) {
	version := firstMatch(regexp.MustCompile(`(?:^|v)([0-9]+\.[0-9]+)`), read(root, ".python-version"), "")
	if version == "" {
		version = firstMatch(
			regexp.MustCompile(`(?i)(?:requires-python|python)\s*=\s*["'][^0-9]*([0-9]+\.[0-9]+)`),
			read(root, "pyproject.toml"),
			"3.12",
		)
	}
	deps := filesMatching(root, "requirements.txt", "requirements-prod.txt", "pyproject.toml", "Pipfile", "Pipfile.lock")
	install := ""
	runtimeVenv := false
	packageManager := "pip"
	if exists(root, "requirements.txt") {
		install = "pip install --prefix=/install -r requirements.txt"
	} else if exists(root, "requirements-prod.txt") {
		install = "pip install --prefix=/install -r requirements-prod.txt"
	} else if exists(root, "setup.py") {
		install = "pip install --prefix=/install ."
	} else if exists(root, "pyproject.toml") {
		content := read(root, "pyproject.toml")
		if strings.Contains(content, "[tool.poetry]") || exists(root, "poetry.lock") {
			if !exists(root, "poetry.lock") {
				return Plan{}, errors.New("Poetry project needs poetry.lock for a reproducible dependency install")
			}
			install = "pip install poetry && poetry config virtualenvs.in-project true && poetry install --only main --no-interaction --no-root"
			runtimeVenv = true
			packageManager = "Poetry"
			deps = filesMatching(root, "pyproject.toml", "poetry.lock")
		} else if exists(root, "uv.lock") || strings.Contains(content, "[tool.uv]") {
			if !exists(root, "uv.lock") {
				return Plan{}, errors.New("uv project needs uv.lock for a reproducible dependency install")
			}
			install = "pip install uv && UV_PROJECT_ENVIRONMENT=/app/.venv UV_CACHE_DIR=/root/.cache/pip/uv uv sync --frozen --no-dev --no-install-project"
			runtimeVenv = true
			packageManager = "uv"
			deps = filesMatching(root, "pyproject.toml", "uv.lock")
		} else {
			install = "pip install --prefix=/install ."
		}
	} else if exists(root, "Pipfile") {
		if !exists(root, "Pipfile.lock") {
			return Plan{}, errors.New("Pipenv project needs Pipfile.lock for a reproducible dependency install")
		}
		install = "pip install pipenv && PIPENV_VENV_IN_PROJECT=1 pipenv install --deploy --ignore-pipfile"
		runtimeVenv = true
		packageManager = "Pipenv"
	}
	port := 8000
	var start []string
	if exists(root, "manage.py") {
		start = []string{"python", "manage.py", "runserver", "0.0.0.0:8000"}
	} else {
		depsText := strings.ToLower(strings.Join([]string{
			read(root, "requirements.txt"),
			read(root, "requirements-prod.txt"),
			read(root, "pyproject.toml"),
			read(root, "Pipfile"),
		}, "\n"))
		if strings.Contains(depsText, "fastapi") && exists(root, "main.py") {
			start = []string{"python", "-m", "uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"}
		} else if strings.Contains(depsText, "flask") && exists(root, "app.py") {
			start = []string{"flask", "--app", "app", "run", "--host=0.0.0.0", "--port=8000"}
		} else {
			for _, candidate := range []string{"main.py", "app.py", "server.py", "src/main.py", "app/main.py"} {
				if exists(root, candidate) {
					module := strings.TrimSuffix(filepath.ToSlash(candidate), ".py")
					module = strings.ReplaceAll(module, "/", ".")
					if strings.Contains(module, ".") && (strings.HasPrefix(candidate, "src/") || strings.HasPrefix(candidate, "app/")) {
						start = []string{"python", "-m", module}
					} else {
						start = []string{"python", candidate}
					}
					break
				}
			}
		}
	}
	if len(start) == 0 {
		return Plan{}, errors.New("Python project has no recognizable start target (expected manage.py, main.py, or app.py); build stopped")
	}
	installAfterCopy := (exists(root, "pyproject.toml") || exists(root, "setup.py")) &&
		!exists(root, "requirements.txt") && !exists(root, "requirements-prod.txt") && !runtimeVenv
	if !installAfterCopy && !runtimeVenv {
		installAfterCopy = pythonRequirementsUseLocalPath(root)
	}
	if runtimeVenv && len(start) > 0 && start[0] == "python" {
		start[0] = "/app/.venv/bin/python"
	}
	return Plan{
		Provider:         "python",
		Language:         "Python",
		Version:          version,
		BuilderImage:     "python:" + version + "-slim-bookworm",
		RuntimeImage:     "python:" + version + "-slim-bookworm",
		RuntimeKind:      "python",
		PackageManager:   packageManager,
		InstallCommand:   install,
		InstallAfterCopy: installAfterCopy,
		RuntimeVenv:      runtimeVenv,
		StartCommand:     start,
		Port:             port,
		DependencyFiles:  deps,
	}, nil
}

func pythonRequirementsUseLocalPath(root string) bool {
	for _, name := range []string{"requirements.txt", "requirements-prod.txt"} {
		for _, line := range strings.Split(read(root, name), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "-e ") || strings.HasPrefix(line, "--editable ") || strings.HasPrefix(line, "--editable=") {
				return true
			}
			if strings.HasPrefix(line, ".") {
				return true
			}
		}
	}
	return false
}
