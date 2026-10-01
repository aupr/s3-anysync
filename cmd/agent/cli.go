package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/niceit/s3-anysync/internal/app"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/storage"
)

const usage = `usage: s3-anysync-agent <command> [flags] [arguments]

Flags always come before arguments.

  machine [--name NAME]
  profile add --name N --bucket B --access-key K [--provider P] [--endpoint URL] [--region R] [--prefix P] [--path-style]
              (secret key from S3_ANYSYNC_SECRET)
  profile list
  profile test PROFILE_ID
  action create --profile ID --name N [--direction D] [--no-propagate-deletes] [--receiver-restores-cloud] [--encrypt] [--exclude GLOB]... [--retention N]
              (passphrase from S3_ANYSYNC_PASSPHRASE when --encrypt)
  action list
  action discover --profile ID
  action join --profile ID ACTION_ID      (passphrase from S3_ANYSYNC_PASSPHRASE for encrypted actions)
  action direction ACTION_ID two-way|local-to-cloud|cloud-to-local|default
  item add --remote-name NAME --type file|dir --path LOCAL_PATH [--exclude GLOB]... ACTION_ID
  item map --path LOCAL_PATH ACTION_ID ITEM_ID
  sync ACTION_ID
  snapshot ACTION_ID
  snapshots ACTION_ID
  restore --to local|cloud|both [--path P]... ACTION_ID TS
  conflicts ACTION_ID
  resolve (--all local|cloud | --path P=local|cloud ...) ACTION_ID

Environment: S3_ANYSYNC_DATA_DIR overrides the data folder.
`

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

type cli struct {
	app *app.App
	out io.Writer
}

type command func(*cli, context.Context, []string) error

func run(args []string, stdout, stderr io.Writer, open func() (*app.App, error)) int {
	commands := map[string]command{
		"machine": (*cli).machine, "profile": (*cli).profile, "action": (*cli).action, "item": (*cli).item,
		"sync": (*cli).sync, "snapshot": (*cli).snapshot, "snapshots": (*cli).snapshots,
		"restore": (*cli).restore, "conflicts": (*cli).conflicts, "resolve": (*cli).resolve,
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	a, err := open()
	if err != nil {
		reportError(stderr, err)
		return 1
	}
	defer a.Close()
	if err := cmd(&cli{app: a, out: stdout}, context.Background(), args[1:]); err != nil {
		reportError(stderr, err)
		return 1
	}
	return 0
}

func reportError(w io.Writer, err error) {
	fmt.Fprintln(w, "error:", err)
	if hint := explain(err); hint != "" {
		fmt.Fprintln(w, hint)
	}
}

// explain returns a one-line explanation of well-known errors, or "".
func explain(err error) string {
	switch {
	case errors.Is(err, engine.ErrKeyCheckMissing):
		return "  hint: the action's passphrase check file (.meta/keycheck) is missing from the cloud, so the passphrase cannot be verified; restore .meta/keycheck before syncing."
	case errors.Is(err, engine.ErrDecrypt):
		return "  hint: the passphrase is wrong or the cloud data is damaged; check S3_ANYSYNC_PASSPHRASE."
	case errors.Is(err, engine.ErrPassphraseMissing):
		return "  hint: this action is encrypted; set S3_ANYSYNC_PASSPHRASE to its passphrase and run `action join` again."
	case errors.Is(err, storage.ErrLocked):
		return "  hint: another machine is syncing this action; try again in a minute."
	case errors.Is(err, storage.ErrLockLost):
		return "  hint: this machine lost the cloud lock during the run (it took too long or the connection dropped); nothing is corrupted, run the sync again."
	}
	return ""
}

// parseArgs parses flags and checks the positional arguments by name.
func parseArgs(fs *flag.FlagSet, args []string, positional ...string) ([]string, error) {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("%s: %w", fs.Name(), err)
	}
	if fs.NArg() != len(positional) {
		if len(positional) == 0 {
			return nil, fmt.Errorf("%s takes no arguments", fs.Name())
		}
		return nil, fmt.Errorf("%s: expected %s", fs.Name(), strings.Join(positional, " "))
	}
	return fs.Args(), nil
}

func (c *cli) table() *tabwriter.Writer { return tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0) }

func (c *cli) machine(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("machine", flag.ContinueOnError)
	name := fs.String("name", "", "new friendly name")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if *name != "" {
		if err := c.app.SetMachineName(*name); err != nil {
			return err
		}
	}
	fmt.Fprintf(c.out, "machine id: %s\nmachine name: %s\n", c.app.MachineID, c.app.MachineName)
	return nil
}

func (c *cli) profile(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("profile: expected add, list or test")
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("profile add", flag.ContinueOnError)
		var p model.Profile
		fs.StringVar(&p.Name, "name", "", "profile name")
		fs.StringVar(&p.Provider, "provider", "custom", "provider label")
		fs.StringVar(&p.Endpoint, "endpoint", "", "endpoint URL (empty for AWS)")
		fs.StringVar(&p.Region, "region", "", "region")
		fs.StringVar(&p.Bucket, "bucket", "", "bucket")
		fs.StringVar(&p.RootPrefix, "prefix", "", "folder inside the bucket")
		fs.StringVar(&p.AccessKeyID, "access-key", "", "access key id")
		fs.BoolVar(&p.PathStyle, "path-style", false, "path-style addressing")
		if _, err := parseArgs(fs, args[1:]); err != nil {
			return err
		}
		secret := os.Getenv("S3_ANYSYNC_SECRET")
		if secret == "" {
			return errors.New("set S3_ANYSYNC_SECRET to the secret access key")
		}
		p, err := c.app.AddProfile(p, secret)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "profile created: %s\n", p.ID)
		return c.printProbe(ctx, p.ID)
	case "list":
		if _, err := parseArgs(flag.NewFlagSet("profile list", flag.ContinueOnError), args[1:]); err != nil {
			return err
		}
		profiles, err := c.app.Profiles()
		if err != nil {
			return err
		}
		w := c.table()
		fmt.Fprintln(w, "ID\tNAME\tBUCKET\tPREFIX\tENDPOINT\tLOCK")
		for _, p := range profiles {
			lock := "best effort"
			if p.ConditionalWrites {
				lock = "conditional"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.Name, p.Bucket, p.RootPrefix, p.Endpoint, lock)
		}
		return w.Flush()
	case "test":
		pos, err := parseArgs(flag.NewFlagSet("profile test", flag.ContinueOnError), args[1:], "PROFILE_ID")
		if err != nil {
			return err
		}
		return c.printProbe(ctx, pos[0])
	}
	return fmt.Errorf("profile: unknown subcommand %q", args[0])
}

func (c *cli) printProbe(ctx context.Context, id string) error {
	res, err := c.app.TestProfile(ctx, id)
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}
	if res.ConditionalWrites {
		fmt.Fprintln(c.out, "connection ok (lock mode: conditional writes)")
	} else {
		fmt.Fprintln(c.out, "connection ok (lock mode: best effort; this provider ignores conditional writes)")
	}
	return nil
}

func (c *cli) action(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("action: expected create, list, discover, join or direction")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("action create", flag.ContinueOnError)
		profile := fs.String("profile", "", "profile id")
		var a model.Action
		fs.StringVar(&a.Name, "name", "", "action name")
		dir := fs.String("direction", string(model.TwoWay), "default direction")
		noDel := fs.Bool("no-propagate-deletes", false, "never delete on the other side")
		fs.BoolVar(&a.ReceiverRestoresCloud, "receiver-restores-cloud", false, "with --no-propagate-deletes: a cloud-to-local machine uploads a file back that the cloud deleted (default: it never uploads)")
		fs.BoolVar(&a.Encrypt, "encrypt", false, "encrypt files")
		var excludes multiFlag
		fs.Var(&excludes, "exclude", "exclude glob (repeatable)")
		fs.IntVar(&a.SnapshotRetention, "retention", model.DefaultSnapshotRetention, "snapshots to keep")
		if _, err := parseArgs(fs, args[1:]); err != nil {
			return err
		}
		a.Direction = model.Direction(*dir)
		a.PropagateDeletes = !*noDel
		a.Excludes = excludes
		created, err := c.app.CreateAction(ctx, *profile, a, os.Getenv("S3_ANYSYNC_PASSPHRASE"))
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "action created: %s\n", created.ID)
		return nil
	case "list":
		if _, err := parseArgs(flag.NewFlagSet("action list", flag.ContinueOnError), args[1:]); err != nil {
			return err
		}
		actions, err := c.app.Actions()
		if err != nil {
			return err
		}
		w := c.table()
		fmt.Fprintln(w, "ID\tNAME\tDIRECTION (THIS MACHINE)\tITEMS\tENCRYPTED")
		for _, a := range actions {
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%v\n", a.ID, a.Name, a.DirectionFor(c.app.MachineID), len(a.Items), a.Encrypt)
		}
		return w.Flush()
	case "discover":
		fs := flag.NewFlagSet("action discover", flag.ContinueOnError)
		profile := fs.String("profile", "", "profile id")
		if _, err := parseArgs(fs, args[1:]); err != nil {
			return err
		}
		found, err := c.app.Discover(ctx, *profile)
		if err != nil {
			return err
		}
		w := c.table()
		fmt.Fprintln(w, "ID\tNAME\tITEMS\tJOINED")
		for _, a := range found {
			_, err := c.app.DB.GetAction(a.ID)
			fmt.Fprintf(w, "%s\t%s\t%d\t%v\n", a.ID, a.Name, len(a.Items), err == nil)
		}
		return w.Flush()
	case "join":
		fs := flag.NewFlagSet("action join", flag.ContinueOnError)
		profile := fs.String("profile", "", "profile id")
		pos, err := parseArgs(fs, args[1:], "ACTION_ID")
		if err != nil {
			return err
		}
		a, err := c.app.Join(ctx, *profile, pos[0], os.Getenv("S3_ANYSYNC_PASSPHRASE"))
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "joined action: %s\n", a.ID)
		w := c.table()
		fmt.Fprintln(w, "ITEM_ID\tREMOTE NAME\tTYPE\tLOCAL PATH")
		for _, it := range a.Items {
			local := it.LocalPaths[c.app.MachineID]
			if local == "" {
				local = "not mapped on this machine (use: item map)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", it.ID, it.RemoteName, it.Type, local)
		}
		return w.Flush()
	case "direction":
		pos, err := parseArgs(flag.NewFlagSet("action direction", flag.ContinueOnError), args[1:], "ACTION_ID", "DIRECTION")
		if err != nil {
			return err
		}
		d := model.Direction(pos[1])
		if pos[1] == "default" {
			d = ""
		}
		if err := c.app.SetMachineDirection(ctx, pos[0], d); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "direction updated")
		return nil
	}
	return fmt.Errorf("action: unknown subcommand %q", args[0])
}

func (c *cli) item(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("item: expected add or map")
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("item add", flag.ContinueOnError)
		var it model.Item
		fs.StringVar(&it.RemoteName, "remote-name", "", "name under current/")
		typ := fs.String("type", "", "file or dir")
		local := fs.String("path", "", "local path on this machine")
		var excludes multiFlag
		fs.Var(&excludes, "exclude", "exclude glob (repeatable)")
		pos, err := parseArgs(fs, args[1:], "ACTION_ID")
		if err != nil {
			return err
		}
		it.Type = model.ItemType(*typ)
		it.Excludes = excludes
		added, err := c.app.AddItem(ctx, pos[0], it, *local)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "item added: %s\n", added.ID)
		return nil
	case "map":
		fs := flag.NewFlagSet("item map", flag.ContinueOnError)
		local := fs.String("path", "", "local path on this machine")
		pos, err := parseArgs(fs, args[1:], "ACTION_ID", "ITEM_ID")
		if err != nil {
			return err
		}
		if err := c.app.MapItem(ctx, pos[0], pos[1], *local); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "item mapped")
		return nil
	}
	return fmt.Errorf("item: unknown subcommand %q", args[0])
}

// runner parses a command that takes exactly ACTION_ID (plus flags in fs).
func (c *cli) runner(fs *flag.FlagSet, args []string, extra ...string) (*engine.Runner, []string, error) {
	pos, err := parseArgs(fs, args, append([]string{"ACTION_ID"}, extra...)...)
	if err != nil {
		return nil, nil, err
	}
	r, err := c.app.Runner(pos[0])
	return r, pos, err
}

func (c *cli) printResult(res model.SyncResult) {
	fmt.Fprintf(c.out, "%s: %s\n", res.Outcome, res.Summary())
	for _, cf := range res.Conflicts {
		fmt.Fprintf(c.out, "  conflict: %s\n", cf.Path)
	}
	for _, p := range res.Deferred {
		fmt.Fprintf(c.out, "  deferred (changed during sync, will be re-planned): %s\n", p)
	}
}

// printConflictHint lists conflicts and tells the user how to continue.
func (c *cli) printConflictHint(list []model.Conflict) {
	for _, cf := range list {
		fmt.Fprintf(c.out, "  conflict: %s\n", cf.Path)
	}
	fmt.Fprintln(c.out, "To continue: review the conflicts (conflicts ACTION_ID), then run resolve again with a choice for every one.")
}

func (c *cli) sync(ctx context.Context, args []string) error {
	r, _, err := c.runner(flag.NewFlagSet("sync", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	res, err := r.Sync(ctx)
	if err != nil {
		return err
	}
	c.printResult(res)
	return nil
}

func (c *cli) snapshot(ctx context.Context, args []string) error {
	r, _, err := c.runner(flag.NewFlagSet("snapshot", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	info, err := r.Snapshot(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "snapshot taken: %s (%d files)\n", info.TS, len(info.Files))
	return nil
}

func (c *cli) snapshots(ctx context.Context, args []string) error {
	r, _, err := c.runner(flag.NewFlagSet("snapshots", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	list, err := r.ListSnapshots(ctx)
	if err != nil {
		return err
	}
	machines := r.Action().Machines
	w := c.table()
	fmt.Fprintln(w, "TS\tREASON\tMACHINE\tFILES")
	for _, s := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", s.TS, s.Reason, nameOr(machines, s.MachineID), len(s.Files))
	}
	return w.Flush()
}

func (c *cli) restore(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	to := fs.String("to", "", "local, cloud or both")
	var only multiFlag
	fs.Var(&only, "path", "restore only this remote path (repeatable)")
	r, pos, err := c.runner(fs, args, "TS")
	if err != nil {
		return err
	}
	res, err := r.Restore(ctx, pos[1], only, engine.RestoreTarget(*to))
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "restored %d file(s)\n", len(res.Restored))
	if engine.RestoreTarget(*to) == engine.RestoreLocal {
		fmt.Fprintln(c.out, "note: restored locally only; a two-way machine will upload these files again at the next sync, and a cloud-to-local machine will re-download the cloud version.")
	}
	for _, p := range res.SkippedUnmapped {
		fmt.Fprintf(c.out, "  skipped (not mapped on this machine): %s\n", p)
	}
	return nil
}

func (c *cli) conflicts(_ context.Context, args []string) error {
	r, _, err := c.runner(flag.NewFlagSet("conflicts", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	list, err := r.Conflicts()
	if err != nil {
		return err
	}
	machines := r.Action().Machines
	w := c.table()
	fmt.Fprintln(w, "PATH\tLOCAL\tCLOUD\tCLOUD WRITTEN BY")
	for _, cf := range list {
		writer := ""
		if cf.Remote != nil {
			writer = nameOr(machines, cf.Remote.Writer)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", cf.Path, describe(cf.Local), describe(cf.Remote), writer)
	}
	return w.Flush()
}

func (c *cli) resolve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	all := fs.String("all", "", "resolve every conflict: local or cloud")
	var each multiFlag
	fs.Var(&each, "path", "PATH=local|cloud (repeatable)")
	r, _, err := c.runner(fs, args)
	if err != nil {
		return err
	}
	choices := map[string]model.Side{}
	if *all != "" {
		list, err := r.Conflicts()
		if err != nil {
			return err
		}
		for _, cf := range list {
			choices[cf.Path] = model.Side(*all)
		}
	}
	for _, kv := range each {
		p, side, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--path %q: expected PATH=local or PATH=cloud", kv)
		}
		choices[p] = model.Side(side)
	}
	if *all == "" && len(each) == 0 {
		return errors.New("nothing to resolve: pass --all local|cloud or --path PATH=local|cloud")
	}
	res, err := r.Resolve(ctx, choices)
	if err != nil {
		return err
	}
	c.printResult(res)
	if res.Outcome == model.OutcomeConflict {
		c.printConflictHint(res.Conflicts)
	}
	return nil
}

func describe(e *model.FileEntry) string {
	if e == nil {
		return "deleted"
	}
	return fmt.Sprintf("%d bytes, %s", e.Size, time.Unix(0, e.MTime).Format("2006-01-02 15:04:05"))
}

func nameOr(names map[string]string, id string) string {
	if n := names[id]; n != "" {
		return n
	}
	return id
}
