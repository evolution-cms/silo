package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectClosestRootAndRejectGenericPHP(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "core", "custom"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"index.php": "<?php", "core/bootstrap.php": "<?php", "core/composer.json": `{"name":"some/php-app"}`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Detect(root); err == nil {
		t.Fatal("generic PHP app accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "core/composer.json"), []byte(`{"name":"evolution-cms/evolution"}`), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := Detect(filepath.Join(root, "core", "custom"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != want {
		t.Fatalf("root = %s; want %s", p.Root, want)
	}
	if p.ID == Identity(filepath.Join(filepath.Dir(root), "other", filepath.Base(root))) {
		t.Fatal("same basename collides")
	}
	if err := os.WriteFile(filepath.Join(root, "core/composer.json"), []byte("{"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Detect(root); err == nil {
		t.Fatal("malformed composer accepted")
	}
}

func TestDetectSymlinkUsesCanonicalIdentity(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "core"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"index.php": "<?php", "core/bootstrap.php": "<?php", "core/composer.json": `{"name":"evolutioncms/evolution"}`} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	a, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Detect(link)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("alias creates a different project: %#v != %#v", a, b)
	}
}
