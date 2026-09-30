package docker

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestComposeUsesExplicitExternalState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "space $state")
	args := ComposeArgs(dir, "abc123", "up", "-d")
	want := []string{"compose", "--project-name", "silo-abc123", "--project-directory", dir,
		"--env-file", filepath.Join(dir, "environment"), "-f", filepath.Join(dir, "compose.yaml"), "up", "-d"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v", args)
	}
}

func TestAmbientConfigurationCannotRedirectComposeOrDatabase(t *testing.T) {
	env := []string{"PATH=/bin", "COMPOSE_FILE=production.yml", "compose_project_name=production", "DB_PASSWORD=other", "MARIADB_ROOT_PASSWORD=other", "APP_ENV=production", "DOCKER_CONTEXT=desktop-linux"}
	want := []string{"PATH=/bin", "DOCKER_CONTEXT=desktop-linux"}
	if got := Environment(env); !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %#v", got)
	}
}
