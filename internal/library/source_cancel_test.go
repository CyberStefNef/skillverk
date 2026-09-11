//go:build !windows

package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCancelSourceStopsTransportAndRemovesCheckout(t *testing.T) {
	root := tempDir(t)
	bin := filepath.Join(root, "bin")
	must(t, os.MkdirAll(bin, 0755))
	git := filepath.Join(bin, "git")
	must(t, os.WriteFile(git, []byte("#!/bin/sh\nsleep 60 &\nwait\n"), 0755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := SourceContext(ctx, "https://fixture.invalid/skills.git")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("cancel not propagated", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("transport did not stop promptly")
	}
	paths, e := filepath.Glob(filepath.Join(root, "cache", "skillverk", "tmp", "source-*"))
	must(t, e)
	if len(paths) != 0 {
		t.Fatal("cancelled checkout leaked", paths)
	}
}
