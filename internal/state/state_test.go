package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evolution-cms/silo/internal/project"
)

func fixture(t *testing.T) Store {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := project.Project{Root: root, ID: project.Identity(root)}
	s, err := Open(t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStateIsExternalStableAndNonDestructive(t *testing.T) {
	s := fixture(t)
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := s.Lock(); err == nil {
		t.Fatal("concurrent lifecycle lock accepted")
	}
	m, err := s.Ensure(8123)
	if err != nil {
		t.Fatal(err)
	}
	env1, err := os.ReadFile(filepath.Join(s.Dir, "environment"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(env1), "DB_PASSWORD=secret") {
		t.Fatal("default password reused")
	}
	again, err := s.Ensure(0)
	if err != nil {
		t.Fatal(err)
	}
	if m != again {
		t.Fatal("metadata changed on restart")
	}
	env2, err := os.ReadFile(filepath.Join(s.Dir, "environment"))
	if err != nil {
		t.Fatal(err)
	}
	if string(env1) != string(env2) {
		t.Fatal("credentials changed on restart")
	}
	changed, err := s.Ensure(9000)
	if err != nil || changed.Port != 9000 {
		t.Fatalf("explicit port update failed: %+v %v", changed, err)
	}
	retained, err := s.Ensure(0)
	if err != nil || retained != changed {
		t.Fatalf("updated port not retained: %+v %v", retained, err)
	}
	entries, err := os.ReadDir(s.Project.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("state generation wrote to application")
	}
	if err := os.Remove(filepath.Join(s.Dir, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(0); err == nil {
		t.Fatal("damaged state silently recreated")
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, "environment"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(env1) {
		t.Fatal("damaged state rotated credentials")
	}
}

func TestRejectStateInsideProjectAndSymlinkAlias(t *testing.T) {
	s := fixture(t)
	if _, err := Open(filepath.Join(s.Project.Root, ".silo"), s.Project); err == nil {
		t.Fatal("state inside project accepted")
	}
	if _, err := Open("relative-home", s.Project); err == nil {
		t.Fatal("relative state home accepted")
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(s.Project.Root, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Open(filepath.Join(link, "missing", "state"), s.Project); err == nil {
		t.Fatal("symlink state inside project accepted")
	}
}

func TestUnlockIsIdempotent(t *testing.T) {
	s := fixture(t)
	first, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	first()
	second, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	first()
	if _, err := s.Lock(); err == nil {
		t.Fatal("repeated unlock removed another process lock")
	}
}

func TestPortUpdatePreservesStateAndRepairsInterruptedUpdate(t *testing.T) {
	s := fixture(t)
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	m, err := s.Ensure(8123)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := os.ReadFile(filepath.Join(s.Dir, "environment"))
	runtime, _ := os.ReadFile(filepath.Join(s.Dir, "runtime", "Dockerfile"))
	path := filepath.Join(s.Dir, "compose.yaml")
	data, _ := os.ReadFile(path)
	var model map[string]any
	if err := json.Unmarshal(data, &model); err != nil {
		t.Fatal(err)
	}
	model["x-test"] = "preserved"
	data, _ = json.Marshal(model)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := s.Ensure(18123)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Project != m.Project {
		t.Fatal("project identity changed")
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "127.0.0.1:18123:80") || !strings.Contains(string(data), "preserved") {
		t.Fatal("binding or unrelated Compose fields lost")
	}
	if current, _ := os.ReadFile(filepath.Join(s.Dir, "environment")); string(current) != string(env) {
		t.Fatal("credentials changed")
	}
	if current, _ := os.ReadFile(filepath.Join(s.Dir, "runtime", "Dockerfile")); string(current) != string(runtime) {
		t.Fatal("runtime changed")
	}
	// Simulate interruption after Compose changed but before metadata committed.
	data = []byte(strings.ReplaceAll(string(data), "127.0.0.1:18123:80", "127.0.0.1:19123:80"))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(0); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "127.0.0.1:18123:80") {
		t.Fatal("saved metadata did not reconcile interrupted update")
	}
	for _, port := range []int{-1, 65536} {
		if _, err := s.Ensure(port); err == nil {
			t.Fatal("invalid port accepted")
		}
	}
	if current, _ := os.ReadFile(path); string(current) != string(data) {
		t.Fatal("invalid port changed Compose")
	}
}

func TestPortUpdateRollsBackWhenMetadataCannotBeReplaced(t *testing.T) {
	s := fixture(t)
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	m, err := s.Ensure(8123)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(s.Dir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Dir, "metadata.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.updatePort(m, 18123); err == nil {
		t.Fatal("metadata replacement failure ignored")
	}
	current, err := os.ReadFile(filepath.Join(s.Dir, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Fatal("Compose not rolled back")
	}
}

func TestComposeModelSeparatesSecretsAndEscapesPaths(t *testing.T) {
	s := fixture(t)
	s.Project.Root = filepath.Join(s.Project.Root, "project $dollar space")
	data, err := composeJSON(Metadata{Project: s.Project, Port: 8087})
	if err != nil {
		t.Fatal(err)
	}
	var model struct {
		Services map[string]struct {
			Ports       []string          `json:"ports"`
			Environment map[string]string `json:"environment"`
			Volumes     []json.RawMessage `json:"volumes"`
		} `json:"services"`
	}
	if err := json.Unmarshal(data, &model); err != nil {
		t.Fatal(err)
	}
	web, db := model.Services["web"], model.Services["db"]
	if len(db.Ports) != 0 {
		t.Fatal("database exposed to host")
	}
	if web.Ports[0] != "127.0.0.1:8087:80" {
		t.Fatal("HTTP exposed beyond localhost")
	}
	if _, ok := web.Environment["MARIADB_ROOT_PASSWORD"]; ok {
		t.Fatal("web gets DB root password")
	}
	if !strings.Contains(string(web.Volumes[0]), "$$dollar") || !strings.Contains(string(web.Volumes[0]), `"create_host_path": false`) {
		t.Fatal("unsafe bind path")
	}
}

func TestDatabaseExampleIsDataNotExecutableConfiguration(t *testing.T) {
	s := fixture(t)
	dir := filepath.Join(s.Project.Root, "core", "custom")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".env.docker.example")
	if err := os.WriteFile(path, []byte("DB_DATABASE='local_evo'\nDB_PASSWORD=do-not-copy\nUNRELATED=$(exit 99)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	unlock, err := s.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := s.Ensure(0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, "environment"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "DB_DATABASE=local_evo\n") || strings.Contains(string(data), "do-not-copy") {
		t.Fatal("wrong example handling")
	}
	if err := os.WriteFile(path, []byte("DB_DATABASE=$(exit 99)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := databaseName(s.Project.Root); err == nil {
		t.Fatal("non-literal DB name accepted")
	}
}
