package library

import (
	"os"
	"path/filepath"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMetadataAcceptsRepositoryAliasButRejectsSubdirectory(t *testing.T) {
	root := tempDir(t)
	repository := repo(t, root, "repository")
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(repository, alias); err != nil {
		t.Skip(err)
	}
	if _, err := metadata(alias); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(repository, "child")
	must(t, os.Mkdir(child, 0755))
	if _, err := metadata(child); err == nil {
		t.Fatal("nested directory accepted as repository root")
	}
}
