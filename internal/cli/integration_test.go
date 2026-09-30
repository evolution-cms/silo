package cli

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/evolution-cms/silo/internal/bootstrap"
	"github.com/evolution-cms/silo/internal/docker"
	"github.com/evolution-cms/silo/internal/project"
	"github.com/evolution-cms/silo/internal/state"
)

// TestDockerLifecycle is opt-in: it only mounts a newly-created test fixture.
// It never reads or starts a user's application and removes only its own DB volume.
func TestDockerLifecycle(t *testing.T) {
	if os.Getenv("SILO_INTEGRATION") != "1" {
		t.Skip("set SILO_INTEGRATION=1 to build and test with Docker")
	}
	root := filepath.Join(t.TempDir(), "Evolution $fixture with spaces")
	if err := os.MkdirAll(filepath.Join(root, "core"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"core/bootstrap.php": "<?php // detection fixture, not a CMS installation\n",
		"core/composer.json": `{"name":"evolution-cms/evolution"}`,
		".htaccess":          "RewriteEngine On\nRewriteRule ^silo-check$ index.php [L]\n",
		"index.php": `<?php
header('Content-Type: application/json');
$db = new PDO('mysql:host='.getenv('DB_HOST').';dbname='.getenv('DB_DATABASE'), getenv('DB_USERNAME'), getenv('DB_PASSWORD'), [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
$db->exec('CREATE TABLE IF NOT EXISTS silo_probe (id INT PRIMARY KEY, marker VARCHAR(64) NOT NULL)');
$db->exec("INSERT IGNORE INTO silo_probe (id, marker) VALUES (1, 'persistent-fixture')");
echo json_encode(['php' => PHP_MAJOR_VERSION.'.'.PHP_MINOR_VERSION, 'marker' => $db->query('SELECT marker FROM silo_probe WHERE id=1')->fetchColumn(), 'extensions' => array_values(array_filter(['gd','mysqli','pdo_mysql','zip','mbstring','intl'], 'extension_loaded'))]);
`,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	addBackup(t, root, true)
	before := snapshot(t, root)
	home := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	d := docker.Client{Out: &output, Err: &output}
	p, err := project.Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := state.Open(home, p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Store and Compose identity come exclusively from this newly-created fixture.
		if _, err := os.Stat(filepath.Join(s.Dir, "compose.yaml")); err == nil {
			if err := d.Compose(s.Dir, p.ID, "down", "--volumes"); err != nil {
				t.Errorf("fixture cleanup: %v\n%s", err, output.String())
			}
		}
	}()
	run := func(args ...string) {
		t.Helper()
		if err := execute(args, "integration", root, home, d, &output); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, output.String())
		}
	}
	run("doctor")
	run("up", "-d", "--port", strconv.Itoa(port))
	if probe(t, port).Marker != "restored-backup" {
		t.Fatal("latest backup not restored before web startup")
	}
	// Change data independently; restart must not recreate the database.
	if err := d.Compose(s.Dir, p.ID, "exec", "-T", "web", "php", "-r", `$db = new PDO('mysql:host='.getenv('DB_HOST').';dbname='.getenv('DB_DATABASE'), getenv('DB_USERNAME'), getenv('DB_PASSWORD')); $db->exec("UPDATE silo_probe SET marker='survived-restart' WHERE id=1");`); err != nil {
		t.Fatalf("update fixture: %v\n%s", err, output.String())
	}
	// A second application shares SILO_HOME, but not Compose identity or data.
	root2 := filepath.Join(t.TempDir(), filepath.Base(root))
	if err := os.MkdirAll(filepath.Join(root2, "core"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root2, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	addBackup(t, root2, false)
	before2 := snapshot(t, root2)
	p2, err := project.Detect(root2)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := state.Open(home, p2)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == p2.ID || s.Dir == s2.Dir {
		t.Fatal("projects not isolated")
	}
	defer func() {
		if _, err := os.Stat(filepath.Join(s2.Dir, "compose.yaml")); err == nil {
			if err := d.Compose(s2.Dir, p2.ID, "down", "--volumes"); err != nil {
				t.Errorf("second fixture cleanup: %v", err)
			}
		}
	}()
	port2 := freePort(t)
	if err := execute([]string{"up", "-d", "--port", strconv.Itoa(port2)}, "integration", root2, home, d, &output); err != nil {
		t.Fatalf("second project: %v\n%s", err, output.String())
	}
	if probe(t, port2).Marker != "restored-backup" {
		t.Fatal("second project shares first database")
	}
	envBefore, err := os.ReadFile(filepath.Join(s.Dir, "environment"))
	if err != nil {
		t.Fatal(err)
	}
	oldPort := port
	port = freePort(t)
	run("up", "-d", "--port", strconv.Itoa(port))
	if probe(t, port).Marker != "survived-restart" {
		t.Fatal("port change lost database content")
	}
	if probe(t, port2).Marker != "restored-backup" {
		t.Fatal("port change affected second project")
	}
	envAfter, err := os.ReadFile(filepath.Join(s.Dir, "environment"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(envBefore, envAfter) {
		t.Fatal("port change rotated credentials")
	}
	connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", oldPort), time.Second)
	if err == nil {
		connection.Close()
		t.Fatal("old HTTP port remains bound")
	}
	run("ps")
	run("down")
	if probe(t, port2).Marker != "restored-backup" {
		t.Fatal("down affected second project")
	}
	run("up", "-d")
	result := probe(t, port)
	if result.Marker != "survived-restart" {
		t.Fatalf("database did not persist: %+v", result)
	}
	if after := snapshot(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("application fixture files changed")
	}
	if after := snapshot(t, root2); !reflect.DeepEqual(before2, after) {
		t.Fatal("second fixture changed")
	}
	t.Log("One-time latest backup restore, PHP/DB lifecycle, live port change, saved-port restart, credentials/data retention, concurrent project isolation and unchanged fixtures verified")
}

func addBackup(t *testing.T, root string, compressed bool) {
	t.Helper()
	dir := filepath.Join(root, "assets", "backup")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "old.sql")
	if err := os.WriteFile(old, []byte("INVALID SQL;"), 0644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, when, when); err != nil {
		t.Fatal(err)
	}
	sql := "CREATE TABLE silo_probe (id INT PRIMARY KEY, marker VARCHAR(64) NOT NULL) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci; INSERT INTO silo_probe VALUES (1, 'restored-backup');"
	if !compressed {
		if err := os.WriteFile(filepath.Join(dir, "latest.sql"), []byte(sql), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.Create(filepath.Join(dir, "latest.sql.gz"))
	if err != nil {
		t.Fatal(err)
	}
	w := gzip.NewWriter(f)
	_, err = io.WriteString(w, sql)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDockerImportFailure(t *testing.T) {
	if os.Getenv("SILO_INTEGRATION") != "1" {
		t.Skip("set SILO_INTEGRATION=1 to test Docker import failures")
	}
	root, home := app(t), t.TempDir()
	dir := filepath.Join(root, "assets", "backup")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.sql"), []byte("CREATE TABLE partial_data (id INT); INSERT INTO partial_data VALUES (1); INVALID SQL;"), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := state.Open(home, p)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	d := docker.Client{Out: &output, Err: &output}
	defer func() {
		if _, err := os.Stat(filepath.Join(s.Dir, "compose.yaml")); err == nil {
			if err := d.Compose(s.Dir, p.ID, "down", "--volumes"); err != nil {
				t.Errorf("failure fixture cleanup: %v", err)
			}
		}
	}()
	if err := execute([]string{"up", "-d", "--port", strconv.Itoa(freePort(t))}, "test", root, home, d, &output); err == nil {
		t.Fatal("invalid SQL import reported success")
	}
	db := bootstrap.DockerDatabase{Runner: d, Dir: s.Dir, ID: p.ID}
	if status, err := db.Status(); err != nil || status != "pending" {
		t.Fatalf("failure marker: %s %v\n%s", status, err, output.String())
	}
	if err := execute([]string{"up", "-d"}, "test", root, home, d, &output); err == nil {
		t.Fatal("partial import retried")
	}
	var web bytes.Buffer
	if err := d.ComposeIO(s.Dir, p.ID, nil, &web, io.Discard, "ps", "-q", "web"); err != nil {
		t.Fatal(err)
	}
	if web.Len() != 0 {
		t.Fatal("web started with incomplete database")
	}
	var data bytes.Buffer
	if err := d.ComposeIO(s.Dir, p.ID, nil, &data, io.Discard, "exec", "-T", "db", "sh", "-c", `env MYSQL_PWD="$MARIADB_PASSWORD" mariadb --protocol=TCP --host=127.0.0.1 --user="$MARIADB_USER" --database="$MARIADB_DATABASE" --batch --skip-column-names --execute='SELECT COUNT(*) FROM partial_data'`); err != nil {
		t.Fatal(err)
	}
	if string(bytes.TrimSpace(data.Bytes())) != "1" {
		t.Fatal("partial data was not preserved")
	}
	t.Log("Invalid SQL blocks web startup and repeat import while preserving partial data")
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

type probeResult struct {
	PHP        string   `json:"php"`
	Marker     string   `json:"marker"`
	Extensions []string `json:"extensions"`
}

func probe(t *testing.T, port int) probeResult {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	var last error
	for i := 0; i < 30; i++ {
		response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/silo-check", port))
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 8192))
			response.Body.Close()
			var result probeResult
			if readErr == nil && response.StatusCode == 200 && json.Unmarshal(data, &result) == nil && result.PHP == "8.4" && len(result.Extensions) == 6 {
				return result
			}
			err = fmt.Errorf("fixture HTTP %d: expected PHP 8.4/DB/extensions response", response.StatusCode)
		}
		last = err
		time.Sleep(time.Second)
	}
	t.Fatalf("fixture did not become ready: %v", last)
	return probeResult{}
}

func snapshot(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	result := make(map[string][32]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[rel] = sha256.Sum256(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
