// Package state owns infrastructure outside application repositories.
package state

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/evolution-cms/silo/internal/project"
)

//go:embed runtime/*
var assets embed.FS

const Schema = 1
const RuntimeImage = "silo-runtime:8.4-apache-v1"
const DatabaseImage = "public.ecr.aws/docker/library/mariadb:11.4"

type Metadata struct {
	Schema  int             `json:"schema"`
	Project project.Project `json:"project"`
	Port    int             `json:"port"`
	Runtime string          `json:"runtime"`
}

type Store struct {
	Dir     string
	Project project.Project
}

// Open resolves existing symlinks even when the requested home does not exist yet.
func Open(home string, p project.Project) (Store, error) {
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return Store{}, err
		}
		home = filepath.Join(userHome, ".silo")
	}
	if !filepath.IsAbs(home) {
		return Store{}, errors.New("SILO_HOME must be an absolute path outside the application")
	}
	resolved, err := resolvePath(home)
	if err != nil {
		return Store{}, err
	}
	if inside(p.Root, resolved) {
		return Store{}, errors.New("SILO_HOME must be outside the application directory")
	}
	dir, err := resolvePath(filepath.Join(resolved, "projects", p.ID))
	if err != nil {
		return Store{}, err
	}
	if inside(p.Root, dir) {
		return Store{}, errors.New("project state resolves inside the application directory")
	}
	return Store{Dir: dir, Project: p}, nil
}

func resolvePath(path string) (string, error) {
	path = filepath.Clean(path)
	var suffix []string
	for {
		_, err := os.Lstat(path)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf("cannot resolve state path %s", path)
		}
		suffix = append(suffix, filepath.Base(path))
		path = parent
	}
}

func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Lock prevents competing lifecycle operations. A stale lock is deliberately not stolen.
func (s Store) Lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.Dir), 0700); err != nil {
		return nil, err
	}
	path := s.Dir + ".lock"
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot lock project state (%s); another Silo process may be active: %w", path, err)
	}
	_, writeErr := fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return nil, errors.Join(writeErr, closeErr)
	}
	var once sync.Once
	return func() { once.Do(func() { _ = os.Remove(path) }) }, nil
}

// Load never creates or repairs state, including on ps and down.
func (s Store) Load() (Metadata, error) {
	var m Metadata
	data, err := os.ReadFile(filepath.Join(s.Dir, "metadata.json"))
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("invalid state metadata: %w", err)
	}
	if m.Schema != Schema || m.Project != s.Project || m.Runtime != RuntimeImage || m.Port < 1 || m.Port > 65535 {
		return m, errors.New("state metadata is incompatible with this project or Silo version; preserve state and investigate before restarting")
	}
	for _, name := range []string{"compose.yaml", "environment", "runtime/Dockerfile", "runtime/php.ini"} {
		info, err := os.Stat(filepath.Join(s.Dir, name))
		if err != nil {
			return m, fmt.Errorf("incomplete state: %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return m, fmt.Errorf("invalid state file: %s", name)
		}
	}
	return m, nil
}

// Ensure creates or updates state under a lifecycle lock. port=0 retains the
// saved port (or selects the initial default). Credentials never change here.
func (s Store) Ensure(port int) (Metadata, error) {
	if port < 0 || port > 65535 {
		return Metadata{}, errors.New("HTTP port must be between 1 and 65535")
	}
	m, err := s.Load()
	if err == nil {
		if port == 0 {
			port = m.Port
		}
		return s.updatePort(m, port)
	}
	if _, statErr := os.Lstat(s.Dir); statErr == nil || !errors.Is(statErr, os.ErrNotExist) {
		return m, fmt.Errorf("refusing to overwrite existing state: %w", err)
	}
	if port == 0 {
		port = 8080
	}
	if port < 1 || port > 65535 {
		return m, errors.New("HTTP port must be between 1 and 65535")
	}
	database, err := databaseName(s.Project.Root)
	if err != nil {
		return m, err
	}
	password, err := secret()
	if err != nil {
		return m, err
	}
	rootPassword, err := secret()
	if err != nil {
		return m, err
	}
	m = Metadata{Schema: Schema, Project: s.Project, Port: port, Runtime: RuntimeImage}
	compose, err := composeJSON(m)
	if err != nil {
		return m, err
	}
	metadata, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return m, err
	}
	if err := os.MkdirAll(filepath.Dir(s.Dir), 0700); err != nil {
		return m, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(s.Dir), ".silo-staging-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(tmp)
	environment := "APP_ENV=local\nDB_CONNECTION=mysql\nDB_HOST=db\nDB_PORT=3306\nDB_DATABASE=" + database + "\nDB_USERNAME=silo\nDB_PASSWORD=" + password + "\nMARIADB_DATABASE=" + database + "\nMARIADB_USER=silo\nMARIADB_PASSWORD=" + password + "\nMARIADB_ROOT_PASSWORD=" + rootPassword + "\n"
	files := map[string][]byte{"compose.yaml": compose, "metadata.json": metadata, "environment": []byte(environment)}
	for _, name := range []string{"Dockerfile", "php.ini"} {
		data, err := assets.ReadFile("runtime/" + name)
		if err != nil {
			return m, err
		}
		files["runtime/"+name] = data
	}
	if err := os.Mkdir(filepath.Join(tmp, "runtime"), 0700); err != nil {
		return m, err
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), data, 0600); err != nil {
			return m, err
		}
	}
	if err := os.Rename(tmp, s.Dir); err != nil {
		return m, err
	}
	return m, nil
}

// updatePort edits only the managed web binding, preserving other Compose fields.
// Metadata is the committed port: an interrupted two-file update is reconciled
// on the next up, including up without --port. Docker is called only afterwards.
func (s Store) updatePort(m Metadata, port int) (Metadata, error) {
	composePath := filepath.Join(s.Dir, "compose.yaml")
	original, err := os.ReadFile(composePath)
	if err != nil {
		return m, err
	}
	var model map[string]json.RawMessage
	var services map[string]json.RawMessage
	var web map[string]json.RawMessage
	if json.Unmarshal(original, &model) != nil ||
		json.Unmarshal(model["services"], &services) != nil ||
		json.Unmarshal(services["web"], &web) != nil || web == nil {
		return m, errors.New("cannot update HTTP port: expected Silo-generated JSON Compose with a web service")
	}
	binding := "127.0.0.1:" + strconv.Itoa(port) + ":80"
	var current []string
	if json.Unmarshal(web["ports"], &current) == nil && len(current) == 1 && current[0] == binding && m.Port == port {
		return m, nil
	}
	web["ports"], _ = json.Marshal([]string{binding})
	services["web"], err = json.Marshal(web)
	if err != nil {
		return m, err
	}
	model["services"], err = json.Marshal(services)
	if err != nil {
		return m, err
	}
	updated, err := json.MarshalIndent(model, "", "  ")
	if err != nil {
		return m, err
	}
	next := m
	next.Port = port
	metadata, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return m, err
	}
	if err := replaceFile(composePath, updated); err != nil {
		return m, err
	}
	if err := replaceFile(filepath.Join(s.Dir, "metadata.json"), metadata); err != nil {
		rollbackErr := replaceFile(composePath, original)
		return m, fmt.Errorf("cannot save HTTP port: %w", errors.Join(err, rollbackErr))
	}
	return next, nil
}

// replaceFile stages complete contents beside the destination before replacing it.
func replaceFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".silo-update-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func secret() (string, error) {
	var bytes [24]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

var dbName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,63}$`)

// Only a literal DB_DATABASE is accepted; dotenv/shell expansion is never executed.
func databaseName(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "core", "custom", ".env.docker.example"))
	if errors.Is(err, os.ErrNotExist) {
		return "evo", nil
	}
	if err != nil {
		return "", err
	}
	name := "evo"
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(key) != "DB_DATABASE" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		if !dbName.MatchString(value) {
			return "", errors.New("DB_DATABASE in core/custom/.env.docker.example must be a literal database name (letters, numbers, underscores or hyphens)")
		}
		name = value
	}
	return name, nil
}

func composeJSON(m Metadata) ([]byte, error) {
	// Compose still interpolates dollar signs inside JSON strings.
	source := strings.ReplaceAll(filepath.ToSlash(m.Project.Root), "$", "$$")
	model := map[string]any{
		"services": map[string]any{
			"web": map[string]any{
				"build":       map[string]any{"context": "./runtime"},
				"image":       RuntimeImage,
				"pull_policy": "build",
				"ports":       []string{"127.0.0.1:" + strconv.Itoa(m.Port) + ":80"},
				"environment": map[string]string{"APP_ENV": "local", "DB_CONNECTION": "mysql", "DB_HOST": "db", "DB_PORT": "3306", "DB_DATABASE": "${DB_DATABASE:?missing database}", "DB_USERNAME": "silo", "DB_PASSWORD": "${DB_PASSWORD:?missing password}"},
				"volumes":     []any{map[string]any{"type": "bind", "source": source, "target": "/var/www/html", "bind": map[string]any{"create_host_path": false}}},
				"depends_on":  map[string]any{"db": map[string]string{"condition": "service_healthy"}},
			},
			"db": map[string]any{
				"image":       DatabaseImage,
				"environment": map[string]string{"MARIADB_DATABASE": "${MARIADB_DATABASE:?missing database}", "MARIADB_USER": "silo", "MARIADB_PASSWORD": "${MARIADB_PASSWORD:?missing password}", "MARIADB_ROOT_PASSWORD": "${MARIADB_ROOT_PASSWORD:?missing root password}"},
				"volumes":     []string{"database:/var/lib/mysql"},
				"healthcheck": map[string]any{"test": []string{"CMD", "healthcheck.sh", "--connect", "--innodb_initialized"}, "interval": "5s", "timeout": "5s", "retries": 20, "start_period": "15s"},
			},
		},
		"volumes": map[string]any{"database": map[string]any{}},
	}
	return json.MarshalIndent(model, "", "  ")
}
