package engine

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/paths"
	"github.com/niceit/s3-anysync/internal/state"
	"github.com/niceit/s3-anysync/internal/storage"
)

// storeFactory returns a shared bucket and a root prefix for one scenario.
type storeFactory func(t *testing.T) (storage.Storage, string)

// scenarios run against every storage implementation (memory always, S3 when configured).
var scenarios = map[string]func(t *testing.T, f storeFactory){}

func memoryStore(*testing.T) (storage.Storage, string) { return storage.NewMemory(), "cfg" }

func TestScenariosMemory(t *testing.T) { runScenarios(t, memoryStore) }

func runScenarios(t *testing.T, f storeFactory) {
	names := make([]string, 0, len(scenarios))
	for n := range scenarios {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fn := scenarios[n]
		t.Run(n, func(t *testing.T) { fn(t, f) })
	}
}

type harness struct {
	t     *testing.T
	store storage.Storage
	root  string
}

func newHarness(t *testing.T, f storeFactory) *harness {
	s, root := f(t)
	return &harness{t: t, store: s, root: root}
}

// machine simulates one computer: its own home folder, state DB and backups.
type machine struct {
	t      *testing.T
	h      *harness
	id     string
	home   string
	db     *state.DB
	deps   Deps
	runner *Runner
}

func (h *harness) machine(id, passphrase string) *machine {
	t := h.t
	db := newDB(t)
	if err := db.PutProfile(model.Profile{ID: "p", Name: "test", Bucket: "b"}); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	m := &machine{t: t, h: h, id: id, home: home, db: db}
	m.deps = Deps{
		Storage: h.store, Repo: db, MachineID: id, MachineName: "machine " + id, Passphrase: passphrase,
		BackupRoot: filepath.Join(home, "backups"), LockMode: storage.LockConditional,
		Expand: func(p string) (string, error) { return paths.ExpandWith(p, home, func(string) string { return "" }) },
	}
	return m
}

const (
	itemSettings = "settings"
	itemSnippets = "snippets"
)

var defaultPaths = map[string]string{itemSettings: "~/app/settings.json", itemSnippets: "~/app/snippets"}

func testAction(name string) model.Action {
	a := model.Action{ID: "act-" + name, Name: name, PropagateDeletes: true, Items: []model.Item{
		{ID: itemSettings, RemoteName: "settings.json", Type: model.ItemFile},
		{ID: itemSnippets, RemoteName: "snippets", Type: model.ItemDir},
	}}
	a.ApplyDefaults()
	return a
}

// create publishes a with every item mapped on m.
func (h *harness) create(m *machine, a model.Action) model.Action {
	h.t.Helper()
	for i := range a.Items {
		a.Items[i].LocalPaths[m.id] = defaultPaths[a.Items[i].ID]
	}
	a.Machines[m.id] = m.deps.MachineName
	a.UpdatedAt = time.Now().UTC()
	if err := CreateSharedAction(ctx, m.deps, h.root, a); err != nil {
		h.t.Fatal(err)
	}
	m.attach(a)
	return a
}

// join maps the given items (item id → local path) on m.
func (h *harness) join(m *machine, actionID string, items map[string]string) model.Action {
	h.t.Helper()
	a, err := UpdateSharedAction(ctx, m.deps, h.root, actionID, func(a *model.Action) error {
		a.Machines[m.id] = m.deps.MachineName
		for i := range a.Items {
			if p, ok := items[a.Items[i].ID]; ok {
				a.Items[i].LocalPaths[m.id] = p
			}
		}
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
	m.attach(a)
	return a
}

func (m *machine) attach(a model.Action) {
	a.ProfileID = "p"
	if err := m.db.PutAction(a); err != nil {
		m.t.Fatal(err)
	}
	m.runner = NewRunner(m.deps, m.h.root, a)
}

func (m *machine) path(rel string) string { return filepath.Join(m.home, filepath.FromSlash(rel)) }

func (m *machine) write(rel, content string) {
	m.t.Helper()
	writeFile(m.t, m.path(rel), content)
}

func (m *machine) read(rel string) string {
	m.t.Helper()
	data, err := os.ReadFile(m.path(rel))
	if err != nil {
		m.t.Fatal(err)
	}
	return string(data)
}

func (m *machine) exists(rel string) bool {
	_, err := os.Stat(m.path(rel))
	return err == nil
}

func (m *machine) remove(rel string) {
	m.t.Helper()
	if err := os.Remove(m.path(rel)); err != nil {
		m.t.Fatal(err)
	}
}

func (m *machine) sync() model.SyncResult {
	m.t.Helper()
	res, err := m.runner.Sync(ctx)
	if err != nil {
		m.t.Fatalf("sync on %s: %v", m.id, err)
	}
	return res
}

func expectCounts(t *testing.T, res model.SyncResult, up, down, delRemote, delLocal int) {
	t.Helper()
	if res.Uploaded != up || res.Downloaded != down || res.DeletedRemote != delRemote || res.DeletedLocal != delLocal {
		t.Fatalf("got %s; want uploaded %d, downloaded %d, deleted in cloud %d, deleted locally %d",
			res.Summary(), up, down, delRemote, delLocal)
	}
}

// syncedPair returns machines A and B that both hold settings.json ({"v":1})
// and snippets/go.json ("go") in sync.
func syncedPair(t *testing.T, f storeFactory, a model.Action) (*harness, *machine, *machine, model.Action) {
	t.Helper()
	h := newHarness(t, f)
	ma, mb := h.machine("A", ""), h.machine("B", "")
	ma.write("app/settings.json", `{"v":1}`)
	ma.write("app/snippets/go.json", "go")
	act := h.create(ma, a)
	expectCounts(t, ma.sync(), 2, 0, 0, 0)
	h.join(mb, act.ID, defaultPaths)
	expectCounts(t, mb.sync(), 0, 2, 0, 0)
	return h, ma, mb, act
}
