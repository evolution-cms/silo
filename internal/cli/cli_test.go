package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeDocker struct {
	calls          [][]string
	failure        error
	composeFailure error
	composeHook    func(string, []string)
}

func (d *fakeDocker) ComposeIO(dir, id string, in io.Reader, out, stderr io.Writer, args ...string) error {
	_, err := io.WriteString(out, "complete")
	return err
}

func (d *fakeDocker) Output(args ...string) (string, error) {
	if d.failure != nil {
		return "", d.failure
	}
	if args[0] == "info" {
		return "linux", nil
	}
	if args[0] == "compose" {
		return "2.20.0", nil
	}
	return "test-version", nil
}

func TestComposeMinimumVersion(t *testing.T) {
	for _, version := range []string{"v2.20.0", "2.39.3-desktop.1", "5.5.1"} {
		if err := checkComposeVersion(version); err != nil {
			t.Fatal(err)
		}
	}
	for _, version := range []string{"1.29.2", "2.19.9", "unknown", ""} {
		if err := checkComposeVersion(version); err == nil {
			t.Fatalf("accepted incompatible Compose %q", version)
		}
	}
}

func (d *fakeDocker) Compose(dir, id string, args ...string) error {
	d.calls = append(d.calls, append([]string{dir, id}, args...))
	if d.composeHook != nil {
		d.composeHook(dir, args)
	}
	return d.composeFailure
}

func TestAttachedUpDoesNotBlockDown(t *testing.T) {
	d := &fakeDocker{composeHook: func(dir string, args []string) {
		if args[0] == "up" && len(args) == 1 {
			if _, err := os.Stat(dir + ".lock"); !os.IsNotExist(err) {
				t.Fatal("attached up holds lifecycle lock, blocking down")
			}
		}
	}}
	if err := execute([]string{"up"}, "test", app(t), t.TempDir(), d, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}

func app(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "core"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"index.php": "<?php", "core/bootstrap.php": "<?php", "core/composer.json": `{"name":"evolution-cms/evolution"}`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLifecycleReusesStateAndKeepsDownNonDestructive(t *testing.T) {
	root, home := app(t), t.TempDir()
	d := &fakeDocker{}
	var out bytes.Buffer
	for _, args := range [][]string{{"up", "--port", "8989", "-d"}, {"ps"}, {"down"}, {"up", "-d"}} {
		if err := execute(args, "test", root, home, d, &out); err != nil {
			t.Fatal(err)
		}
	}
	want := [][]string{{"config", "--quiet"}, {"up", "-d", "--wait", "--wait-timeout", "120", "db"}, {"up", "-d", "--wait", "--wait-timeout", "120"}, {"ps"}, {"down"}, {"config", "--quiet"}, {"up", "-d", "--wait", "--wait-timeout", "120", "db"}, {"up", "-d", "--wait", "--wait-timeout", "120"}}
	if len(d.calls) != len(want) {
		t.Fatalf("calls = %v", d.calls)
	}
	for i, call := range d.calls {
		if !reflect.DeepEqual(call[2:], want[i]) {
			t.Fatalf("call %d: %v", i, call)
		}
		if call[0] == root || !strings.HasPrefix(call[0], home) {
			t.Fatal("Compose uses application directory")
		}
		if call[0] != d.calls[0][0] || call[1] != d.calls[0][1] {
			t.Fatal("state identity changed")
		}
	}
}

func TestFailuresAndReadOnlyCommandsDoNotCreateState(t *testing.T) {
	root := app(t)
	for _, args := range [][]string{{"ps"}, {"down"}, {"up", "--port", "0"}, {"up", "--port", "65536"}, {"down", "-v"}, {"up", "unexpected"}} {
		home := filepath.Join(t.TempDir(), "not-created")
		d := &fakeDocker{failure: errors.New("daemon unavailable")}
		var out bytes.Buffer
		err := execute(args, "test", root, home, d, &out)
		if args[0] == "up" && err == nil {
			t.Fatalf("invalid up accepted: %v", args)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatalf("state created for %v", args)
		}
		if len(d.calls) != 0 {
			t.Fatalf("Compose called for %v", args)
		}
	}
	home := filepath.Join(t.TempDir(), "not-created")
	if err := execute([]string{"up"}, "test", root, home, &fakeDocker{failure: errors.New("offline")}, &bytes.Buffer{}); err == nil {
		t.Fatal("offline daemon ignored")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("offline startup created state")
	}
}

func TestVersionWithoutDockerAndComposeFailurePropagation(t *testing.T) {
	var out bytes.Buffer
	if err := execute([]string{"version"}, "v-test", t.TempDir(), "", &fakeDocker{failure: errors.New("offline")}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "v-test") {
		t.Fatal("version missing")
	}
	failure := errors.New("compose failed")
	err := execute([]string{"up"}, "test", app(t), t.TempDir(), &fakeDocker{composeFailure: failure}, &out)
	if !errors.Is(err, failure) {
		t.Fatalf("Compose error lost: %v", err)
	}
}
