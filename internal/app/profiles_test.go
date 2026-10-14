package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/aupr/s3-anysync/internal/engine"
	"github.com/aupr/s3-anysync/internal/model"
	"github.com/aupr/s3-anysync/internal/secrets"
	"github.com/aupr/s3-anysync/internal/state"
	"github.com/aupr/s3-anysync/internal/storage"
)

func TestUpdateProfile(t *testing.T) {
	a := newTestApp(t, storage.NewMemory())
	p, err := a.AddProfile(model.Profile{Name: "p", Bucket: "b", RootPrefix: "cfg", AccessKeyID: "k"}, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.TestProfile(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	// Blank secret keeps the stored one; name/region change keep ConditionalWrites.
	p.Name, p.Region = "renamed", "eu-1"
	got, err := a.UpdateProfile(p, "")
	if err != nil || got.Name != "renamed" || !got.ConditionalWrites {
		t.Fatalf("UpdateProfile = %+v, %v", got, err)
	}
	if s, _ := a.Secrets.Get(secrets.ProfileSecretKey(p.ID)); s != "s1" {
		t.Fatalf("secret = %q", s)
	}

	// A new secret replaces it; an endpoint change resets ConditionalWrites.
	p.Endpoint = "http://other:9000"
	got, err = a.UpdateProfile(p, "s2")
	if err != nil || got.ConditionalWrites {
		t.Fatalf("UpdateProfile = %+v, %v", got, err)
	}
	if s, _ := a.Secrets.Get(secrets.ProfileSecretKey(p.ID)); s != "s2" {
		t.Fatalf("secret = %q", s)
	}
	if saved, _ := a.DB.GetProfile(p.ID); saved.Endpoint != "http://other:9000" {
		t.Fatalf("saved = %+v", saved)
	}

	// Validation and unknown IDs.
	bad := p
	bad.Bucket = " "
	if _, err := a.UpdateProfile(bad, ""); !errors.Is(err, engine.ErrInvalidInput) {
		t.Fatalf("empty bucket: %v", err)
	}
	if _, err := a.UpdateProfile(model.Profile{ID: "nope", Name: "x", Bucket: "b"}, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestProfileInUse(t *testing.T) {
	a := newTestApp(t, storage.NewMemory())
	p, _ := a.AddProfile(model.Profile{Name: "p", Bucket: "b", RootPrefix: "cfg", AccessKeyID: "k"}, "s")
	if _, err := a.CreateAction(ctx, p.ID, model.Action{Name: "Editor"}, ""); err != nil {
		t.Fatal(err)
	}
	moved := p
	moved.Bucket = "other"
	if _, err := a.UpdateProfile(moved, ""); !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "Editor") {
		t.Fatalf("bucket change in use: %v", err)
	}
	moved = p
	moved.RootPrefix = "elsewhere"
	if _, err := a.UpdateProfile(moved, ""); !errors.Is(err, ErrInUse) {
		t.Fatalf("prefix change in use: %v", err)
	}
	same := p
	same.RootPrefix = "/cfg/" // normalizes to the same prefix: allowed
	if _, err := a.UpdateProfile(same, ""); err != nil {
		t.Fatalf("same prefix: %v", err)
	}
	if err := a.RemoveProfile(p.ID); !errors.Is(err, ErrInUse) || !strings.Contains(err.Error(), "Editor") {
		t.Fatalf("remove in use: %v", err)
	}
}

func TestRemoveProfile(t *testing.T) {
	a := newTestApp(t, storage.NewMemory())
	p, _ := a.AddProfile(model.Profile{Name: "p", Bucket: "b", AccessKeyID: "k"}, "s")
	if err := a.RemoveProfile(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.GetProfile(p.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("profile still there: %v", err)
	}
	if _, err := a.Secrets.Get(secrets.ProfileSecretKey(p.ID)); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("secret still there: %v", err)
	}
	if err := a.RemoveProfile(p.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
}
