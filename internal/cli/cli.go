// Package cli implements Silo's small initial command surface.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/evolution-cms/silo/internal/docker"
	"github.com/evolution-cms/silo/internal/project"
	"github.com/evolution-cms/silo/internal/state"
)

const usage = `Silo — standalone local environments for Evolution CMS

Usage:
  silo version
  silo doctor
  silo up [-d|--detach] [--port 8080]
  silo ps
  silo down

Run from an Evolution project or any directory below it.
State: ~/.silo (override with an absolute external SILO_HOME).
HTTP: http://127.0.0.1:8080 by default; --port selects the first startup port.
down retains database data. No application configuration is generated.
`

type driver interface {
	Output(...string) (string, error)
	Compose(string, string, ...string) error
}

// Run is the process boundary: lifecycle errors preserve the Docker exit code.
func Run(args []string, version string, in io.Reader, out, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err == nil {
		err = execute(args, version, cwd, os.Getenv("SILO_HOME"), docker.Client{In: in, Out: out, Err: stderr}, out)
	}
	if err == nil {
		return 0
	}
	fmt.Fprintln(stderr, "silo:", err)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return exit.ExitCode()
	}
	return 1
}

func execute(args []string, version, cwd, home string, d driver, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, usage)
		return nil
	}
	command := args[0]
	if command != "version" && command != "doctor" && command != "up" && command != "ps" && command != "down" {
		return fmt.Errorf("unknown command %q; run silo --help", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	flags.Usage = func() { fmt.Fprint(out, usage) }
	var detach bool
	var port int
	if command == "up" {
		flags.BoolVar(&detach, "d", false, "start in background")
		flags.BoolVar(&detach, "detach", false, "start in background")
		flags.IntVar(&port, "port", 0, "HTTP port on first startup (default 8080)")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	portSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portSet = true
		}
	})
	if portSet && (port < 1 || port > 65535) {
		return errors.New("--port must be between 1 and 65535")
	}
	if command == "version" {
		fmt.Fprintf(out, "silo %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return nil
	}
	if command == "doctor" {
		return doctor(cwd, home, d, out)
	}
	if !supported(runtime.GOOS, runtime.GOARCH) {
		return fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	p, err := project.Detect(cwd)
	if err != nil {
		return err
	}
	s, err := state.Open(home, p)
	if err != nil {
		return err
	}
	if command != "up" {
		if _, err = s.Load(); errors.Is(err, os.ErrNotExist) {
			// Existing but damaged state is not equivalent to a project never started.
			if _, statErr := os.Stat(s.Dir); errors.Is(statErr, os.ErrNotExist) {
				fmt.Fprintln(out, "No Silo environment exists for this project. Run silo up -d first.")
				return nil
			}
			return err
		} else if err != nil {
			return err
		}
	}
	if err := ready(d); err != nil {
		return err
	}
	if command == "ps" {
		return d.Compose(s.Dir, p.ID, "ps")
	}
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if command == "down" {
		return d.Compose(s.Dir, p.ID, "down")
	}
	m, err := s.Ensure(port)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Project: %s\nState: %s\nHTTP: http://127.0.0.1:%d\n", p.Root, s.Dir, m.Port)
	fmt.Fprintln(out, "Database: isolated local volume; existing application data is not imported.")
	if err := d.Compose(s.Dir, p.ID, "config", "--quiet"); err != nil {
		return fmt.Errorf("invalid Compose configuration: %w", err)
	}
	up := []string{"up"}
	if detach {
		up = append(up, "-d", "--wait", "--wait-timeout", "120")
	} else {
		// Attached Compose may run indefinitely. Allow down from another terminal
		// once state is complete and validated; Compose owns the running services.
		unlock()
	}
	return d.Compose(s.Dir, p.ID, up...)
}

func supported(os, arch string) bool {
	return (os == "windows" && arch == "amd64") || ((os == "linux" || os == "darwin") && (arch == "amd64" || arch == "arm64"))
}

func ready(d driver) error {
	if _, err := d.Output("--version"); err != nil {
		return fmt.Errorf("Docker CLI is unavailable; install Docker with Compose: %w", err)
	}
	composeVersion, err := d.Output("compose", "version", "--short")
	if err != nil {
		return fmt.Errorf("Docker Compose is unavailable: %w", err)
	}
	if err := checkComposeVersion(composeVersion); err != nil {
		return err
	}
	output, err := d.Output("info", "--format", "{{.OSType}}")
	if err != nil {
		return fmt.Errorf("Docker daemon is unavailable; start Docker Desktop or the Docker service: %w", err)
	}
	if strings.TrimSpace(output) != "linux" {
		return errors.New("Silo requires a Linux container engine; switch Docker Desktop to Linux containers")
	}
	return nil
}

func checkComposeVersion(version string) error {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	if len(parts) >= 2 {
		major, errMajor := strconv.Atoi(parts[0])
		minor, errMinor := strconv.Atoi(parts[1])
		if errMajor == nil && errMinor == nil && (major > 2 || (major == 2 && minor >= 20)) {
			return nil
		}
	}
	return fmt.Errorf("Docker Compose 2.20+ is required (found %q); update the Compose plugin", version)
}

func doctor(cwd, home string, d driver, out io.Writer) error {
	failed := false
	report := func(name, message string, err error) {
		label := "OK"
		if err != nil {
			label = "FAIL"
			failed = true
			message = err.Error()
		}
		fmt.Fprintf(out, "[%s] %s: %s\n", label, name, message)
	}
	var platformErr error
	if !supported(runtime.GOOS, runtime.GOARCH) {
		platformErr = errors.New("unsupported OS/architecture")
	}
	report("Platform", runtime.GOOS+"/"+runtime.GOARCH, platformErr)
	for _, check := range []struct {
		name string
		args []string
	}{
		{"Docker", []string{"--version"}}, {"Compose", []string{"compose", "version", "--short"}}, {"Daemon", []string{"info", "--format", "{{.OSType}}"}},
	} {
		value, err := d.Output(check.args...)
		if check.name == "Compose" && err == nil {
			err = checkComposeVersion(value)
		}
		if check.name == "Daemon" && err == nil && value != "linux" {
			err = errors.New("Linux containers required")
		}
		if err != nil {
			err = fmt.Errorf("%w (start/check Docker and its Compose plugin)", err)
		}
		report(check.name, value, err)
	}
	if runtime.GOOS == "windows" {
		fmt.Fprintln(out, "[INFO] WSL: native Windows mode; Docker Desktop manages its backend, WSL is not required by the CLI.")
	} else if runtime.GOOS == "linux" {
		kernel, _ := os.ReadFile("/proc/sys/kernel/osrelease")
		if strings.Contains(strings.ToLower(string(kernel)), "microsoft") || os.Getenv("WSL_DISTRO_NAME") != "" {
			var wslErr error
			if !strings.Contains(strings.ToLower(string(kernel)), "wsl2") {
				wslErr = errors.New("WSL2 kernel not detected; enable WSL2 and Docker integration")
			}
			report("WSL", strings.TrimSpace(string(kernel)), wslErr)
		}
	}
	p, err := project.Detect(cwd)
	report("Evolution", p.Root, err)
	if err == nil {
		s, stateErr := state.Open(home, p)
		report("State location", s.Dir, stateErr)
		_, vendorErr := os.Stat(filepath.Join(p.Root, "core", "vendor", "autoload.php"))
		if vendorErr != nil {
			fmt.Fprintln(out, "[WARN] PHP dependencies: core/vendor/autoload.php is missing; install project dependencies before testing the CMS.")
		}
		fmt.Fprintln(out, "[INFO] Application: DB content and hardcoded PHP configuration are not checked or changed.")
	}
	if failed {
		return errors.New("doctor found issues; resolve the failed checks before silo up")
	}
	return nil
}
