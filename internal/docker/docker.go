// Package docker invokes the installed Docker CLI without a shell.
package docker

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Environment excludes ambient Compose and database overrides. Docker context,
// certificates and credential helper settings remain available to the Docker CLI.
func Environment(environ []string) []string {
	result := make([]string, 0, len(environ))
	for _, item := range environ {
		key, _, _ := strings.Cut(item, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "COMPOSE_") || strings.HasPrefix(key, "DB_") || strings.HasPrefix(key, "MARIADB_") || key == "APP_ENV" {
			continue
		}
		result = append(result, item)
	}
	return result
}

// Output bounds diagnostic commands so an unavailable daemon does not hang doctor.
func (c Client) Output(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = Environment(os.Environ())
	data, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(data), ctx.Err()
	}
	return strings.TrimSpace(string(data)), err
}

func ComposeArgs(dir, id string, args ...string) []string {
	base := []string{"compose", "--project-name", "silo-" + id, "--project-directory", dir,
		"--env-file", filepath.Join(dir, "environment"), "-f", filepath.Join(dir, "compose.yaml")}
	return append(base, args...)
}

// Compose inherits console streams, preserving attached logs and Docker's exit code.
func (c Client) Compose(dir, id string, args ...string) error {
	return c.ComposeIO(dir, id, c.In, c.Out, c.Err, args...)
}

// ComposeIO supports streaming backups without placing SQL or passwords in argv.
func (c Client) ComposeIO(dir, id string, in io.Reader, out, stderr io.Writer, args ...string) error {
	cmd := exec.Command("docker", ComposeArgs(dir, id, args...)...)
	cmd.Dir = dir
	cmd.Env = Environment(os.Environ())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, stderr
	return cmd.Run()
}
