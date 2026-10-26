package build

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/batlify/batlify-packs/internal/plan"
)

// Build streams a filtered tar context to Docker without writing generated files
// into the project directory or sending common local caches to the daemon.
func Build(p plan.Plan, image string, stdout, stderr io.Writer) error {
	return BuildWithOptions(p, image, stdout, stderr, Options{})
}

// Options controls optional Docker build behavior.
type Options struct {
	NoCache bool
}

// BuildWithOptions builds the planned image with the requested Docker options.
func BuildWithOptions(p plan.Plan, image string, stdout, stderr io.Writer, options Options) error {
	dockerfile, err := p.Dockerfile()
	if err != nil {
		return err
	}
	if image == "" || strings.TrimSpace(image) != image || strings.ContainsAny(image, "\r\n\x00") {
		return fmt.Errorf("invalid image name %q", image)
	}
	args := []string{"build", "--tag", image, "--file", generatedDockerfile}
	if options.NoCache {
		args = append(args, "--no-cache")
	}
	args = append(args, "-")
	cmd := exec.Command("docker", args...)
	cmd.Env = withEnv(os.Environ(), "DOCKER_BUILDKIT", "1")
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	pipeReader, pipeWriter := io.Pipe()
	cmd.Stdin = pipeReader
	if err := cmd.Start(); err != nil {
		_ = pipeReader.Close()
		_ = pipeWriter.Close()
		return fmt.Errorf("start Docker build (is Docker installed and the daemon running?): %w", err)
	}
	archiveDone := make(chan error, 1)
	go func() {
		archiveErr := writeContextTar(pipeWriter, p.ProjectDir, generatedDockerfile, dockerfile, p.BuildContextIgnore...)
		_ = pipeWriter.CloseWithError(archiveErr)
		archiveDone <- archiveErr
	}()
	waitErr := cmd.Wait()
	_ = pipeReader.Close()
	archiveErr := <-archiveDone
	if waitErr != nil {
		if archiveErr != nil && !isClosedPipe(archiveErr) {
			return fmt.Errorf("Docker image build failed: %w (build context: %v)", waitErr, archiveErr)
		}
		return fmt.Errorf("Docker image build failed: %w", waitErr)
	}
	if archiveErr != nil {
		return archiveErr
	}
	return nil
}

func isClosedPipe(err error) bool {
	return errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed)
}

func withEnv(environment []string, key, value string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, key+"=") {
			result = append(result, item)
		}
	}
	return append(result, key+"="+value)
}
