package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildArgsRendersEveryOption(t *testing.T) {
	got := BuildArgs(BuildOptions{
		ContextDir: "/src/app",
		Dockerfile: "/src/app/docker/Containerfile",
		Target:     "runtime",
		Platform:   "linux/amd64",
		Tag:        "app:latest",
	})
	want := []string{"build",
		"--platform", "linux/amd64",
		"--file", "/src/app/docker/Containerfile",
		"--target", "runtime",
		"--tag", "app:latest",
		"/src/app",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("BuildArgs:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildArgsOmitsUnsetOptions(t *testing.T) {
	got := BuildArgs(BuildOptions{
		ContextDir: "/src/app",
		Platform:   "linux/arm64",
		Tag:        "app:latest",
	})
	want := []string{"build",
		"--platform", "linux/arm64",
		"--tag", "app:latest",
		"/src/app",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("BuildArgs:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildReportsMissingDocker(t *testing.T) {
	c := CLI{Binary: "sbx-test-no-such-docker"}
	err := c.Build(context.Background(), BuildOptions{ContextDir: ".", Platform: "linux/arm64", Tag: "app:1"}, nil, nil)
	if err == nil {
		t.Fatal("Build succeeded without a docker executable")
	}
	if !strings.Contains(err.Error(), "not installed") || !strings.Contains(err.Error(), "Docker") {
		t.Errorf("error = %q, want actionable missing-Docker guidance", err)
	}
}

func TestSaveReportsMissingDocker(t *testing.T) {
	c := CLI{Binary: "sbx-test-no-such-docker"}
	err := c.Save(context.Background(), "app:1", filepath.Join(t.TempDir(), "image.tar"))
	if err == nil {
		t.Fatal("Save succeeded without a docker executable")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error = %q, want actionable missing-Docker guidance", err)
	}
}

// TestSaveFailureCarriesDetail checks that a failing docker save surfaces
// Docker's own message.
func TestSaveFailureCarriesDetail(t *testing.T) {
	script := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'No such image: app:1' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := CLI{Binary: script}
	err := c.Save(context.Background(), "app:1", filepath.Join(t.TempDir(), "image.tar"))
	if err == nil {
		t.Fatal("Save succeeded for a failing docker")
	}
	if !strings.Contains(err.Error(), "No such image") {
		t.Errorf("error = %q, want Docker's own message", err)
	}
}
