package datalock

import (
	"errors"
	"testing"
)

func TestAcquireIsExclusive(t *testing.T) {
	dir := t.TempDir()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(dir); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Acquire: want ErrBusy, got %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	l2.Release()
}

func TestAcquireCreatesFolder(t *testing.T) {
	dir := t.TempDir() + "/nested/data"
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
}
