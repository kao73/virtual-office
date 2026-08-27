package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeFixtureCommitsFixtureTree(t *testing.T) {
	caseDir := t.TempDir()
	fixtureSrc := filepath.Join(caseDir, "fixture")
	if err := os.MkdirAll(fixtureSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureSrc, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixtureDir, commit, err := materializeFixture(caseDir)
	if err != nil {
		t.Fatalf("fixture не материализован: %v", err)
	}
	defer os.RemoveAll(fixtureDir)

	if commit == "" {
		t.Error("нет initial commit SHA")
	}
	if _, err := os.Stat(filepath.Join(fixtureDir, "hello.txt")); err != nil {
		t.Errorf("файл фикстуры не скопирован: %v", err)
	}
	status, err := exec.Command("git", "-C", fixtureDir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		t.Errorf("рабочее дерево не чистое после коммита: %s", status)
	}
}

func TestMaterializeFixtureFailsWithoutFixtureDir(t *testing.T) {
	caseDir := t.TempDir() // no "fixture" subdirectory
	if _, _, err := materializeFixture(caseDir); err == nil {
		t.Error("отсутствие fixture/ не замечено")
	}
}
