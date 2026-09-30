// Package project discovers Evolution applications without modifying them.
package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Project struct {
	Root string `json:"root"`
	ID   string `json:"id"`
}

// Detect selects the closest Evolution root, including when invoked in core/.
func Detect(start string) (Project, error) {
	root, err := filepath.Abs(start)
	if err != nil {
		return Project{}, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Project{}, err
	}
	for {
		if isFile(filepath.Join(root, "index.php")) && isFile(filepath.Join(root, "core", "bootstrap.php")) {
			data, err := os.ReadFile(filepath.Join(root, "core", "composer.json"))
			var composer struct{ Name string }
			if err == nil && json.Unmarshal(data, &composer) == nil &&
				(composer.Name == "evolution-cms/evolution" || composer.Name == "evolutioncms/evolution") {
				return Project{Root: root, ID: Identity(root)}, nil
			}
		}
		parent := filepath.Dir(root)
		if parent == root {
			return Project{}, fmt.Errorf("no Evolution CMS project found from %s (expected index.php, core/bootstrap.php and core/composer.json)", start)
		}
		root = parent
	}
}

// Identity separates projects by canonical location, not by directory basename.
func Identity(root string) string {
	key := filepath.Clean(root)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:12])
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
