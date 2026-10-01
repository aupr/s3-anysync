package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

var ctx = context.Background()

func newDB(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func fixedClock() func() time.Time {
	t := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func basicAction(id string) model.Action {
	a := model.Action{ID: id, Name: id, PropagateDeletes: true, Items: []model.Item{
		{ID: "settings", RemoteName: "settings.json", Type: model.ItemFile},
	}}
	a.ApplyDefaults()
	return a
}

func memDeps(mem *storage.Memory, machineID string) Deps {
	return Deps{Storage: mem, MachineID: machineID, MachineName: "machine " + machineID, LockMode: storage.LockConditional}
}
