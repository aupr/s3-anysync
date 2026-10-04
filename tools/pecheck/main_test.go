package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func build(t *testing.T, dir, name string, ldflags string) string {
	t.Helper()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", out, src)
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0", "GO111MODULE=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	return out
}

func TestSubsystem(t *testing.T) {
	dir := t.TempDir()
	gui := build(t, dir, "gui.exe", "-H=windowsgui")
	con := build(t, dir, "con.exe", "")
	if s, err := subsystem(gui); err != nil || s != "gui" {
		t.Fatalf("gui = %q, %v", s, err)
	}
	if s, err := subsystem(con); err != nil || s != "console" {
		t.Fatalf("console = %q, %v", s, err)
	}
	if _, err := subsystem(filepath.Join(dir, "main.go")); err == nil {
		t.Fatal("non-PE accepted")
	}
}
