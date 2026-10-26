package build

import (
	"archive/tar"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const generatedDockerfile = "Dockerfile"

type ignoreRule struct {
	negate bool
	regex  *regexp.Regexp
}

var defaultIgnore = []string{
	".git", ".hg", ".svn", ".env", ".env.*", ".DS_Store",
	"config/master.key", "config/credentials/*.key",
	"node_modules", ".venv", "venv", "__pycache__", "*.pyc",
	".dart_tool", ".packages",
	".pytest_cache", ".mypy_cache", ".ruff_cache", ".idea", ".vscode",
	".next", ".npm", "coverage", "vendor",
	"target", "bin", "obj",
}

func writeContextTar(w io.Writer, root, dockerfile, dockerfileContent string, additionalIgnore ...string) error {
	rules, hasNegation, err := loadIgnoreRules(root, additionalIgnore...)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(w)
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == dockerfile || rel == ".dockerignore" {
			return nil
		}
		ignored := matchesIgnore(rel, entry.IsDir(), rules)
		if ignored && !hasNegation && entry.IsDir() {
			return filepath.SkipDir
		}
		if ignored {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = rel
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if walkErr != nil {
		_ = tw.Close()
		return fmt.Errorf("package build context: %w", walkErr)
	}
	ignoreContents, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil && !os.IsNotExist(err) {
		_ = tw.Close()
		return fmt.Errorf("read .dockerignore: %w", err)
	}
	ignoreText := strings.Join(additionalIgnore, "\n")
	if ignoreText != "" {
		ignoreText += "\n"
	}
	ignoreText += strings.ReplaceAll(string(ignoreContents), "\r\n", "\n")
	if ignoreText != "" && !strings.HasSuffix(ignoreText, "\n") {
		ignoreText += "\n"
	}
	ignoreText += "Dockerfile\n.dockerignore\n"
	if err := writeContextFile(tw, ".dockerignore", ignoreText); err != nil {
		_ = tw.Close()
		return fmt.Errorf("add filtered .dockerignore to build context: %w", err)
	}
	if err := writeContextFile(tw, dockerfile, dockerfileContent); err != nil {
		_ = tw.Close()
		return fmt.Errorf("add generated Dockerfile to context: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("finish build context: %w", err)
	}
	return nil
}

func writeContextFile(tw *tar.Writer, name, contents string) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := io.WriteString(tw, contents)
	return err
}

func loadIgnoreRules(root string, additionalIgnore ...string) ([]ignoreRule, bool, error) {
	lines := append([]string(nil), defaultIgnore...)
	lines = append(lines, additionalIgnore...)
	contents, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil && !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("read .dockerignore: %w", err)
	}
	if err == nil {
		lines = append(lines, strings.Split(strings.ReplaceAll(string(contents), "\r\n", "\n"), "\n")...)
	}
	rules := make([]ignoreRule, 0, len(lines))
	hasNegation := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || line == "." {
			continue
		}
		negate := strings.HasPrefix(line, "!")
		if negate {
			line = strings.TrimPrefix(line, "!")
			hasNegation = true
		}
		line = strings.Trim(strings.ReplaceAll(line, "\\", "/"), "/")
		rx, err := globRegex(line)
		if err != nil {
			return nil, false, fmt.Errorf("invalid .dockerignore pattern %q: %w", line, err)
		}
		rules = append(rules, ignoreRule{negate: negate, regex: rx})
	}
	return rules, hasNegation, nil
}

func matchesIgnore(path string, isDir bool, rules []ignoreRule) bool {
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	ignored := false
	for _, rule := range rules {
		matched := false
		for i := range parts {
			candidate := strings.Join(parts[:i+1], "/")
			if rule.regex.MatchString(candidate) {
				matched = true
				break
			}
		}
		if matched {
			ignored = !rule.negate
		}
	}
	return ignored
}

func globRegex(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	if !strings.Contains(pattern, "/") {
		b.WriteString(`(?:^|.*/)`)
	} else {
		b.WriteString(`^`)
	}
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString(`(?:.*/)?`)
				} else {
					b.WriteString(`.*`)
				}
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString(`$`)
	return regexp.Compile(b.String())
}
