package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/niceit/s3-anysync/internal/api"
	"github.com/niceit/s3-anysync/internal/datalock"
	"github.com/niceit/s3-anysync/internal/engine"
	"github.com/niceit/s3-anysync/internal/model"
	"github.com/niceit/s3-anysync/internal/schedule"
	"github.com/niceit/s3-anysync/internal/storage"
)

const usage = `usage: s3-anysync-agent <command> [flags] [arguments]

Flags always come before arguments. When the agent is running, commands go
through it; otherwise they run in this process.

  run [--no-tray] [--verbose]          start the background agent
  status
  machine [--name NAME]
  settings [--log-level debug|info|warn|error]
  autostart enable|disable|status
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
  action rules [--name N] [--direction D] [--on-change[=false]] [--debounce SECONDS] [--schedule SPEC] [--poll MINUTES]
               [--propagate-deletes[=false]] [--receiver-restores-cloud[=false]] [--exclude GLOB]... [--no-excludes]
               [--retention N] [--delete-guard-files N] [--delete-guard-percent P] ACTION_ID
               SPEC: none | every:MINUTES | daily:HH:MM | weekly:DAY@HH:MM (DAY 0=Sunday) | cron:SPEC
               without flags it shows the rules
  action leave [--force] ACTION_ID
  item add --remote-name NAME --type file|dir --path LOCAL_PATH [--exclude GLOB]... ACTION_ID
  item map --path LOCAL_PATH ACTION_ID ITEM_ID
  item unmap ACTION_ID ITEM_ID
  item remove ACTION_ID ITEM_ID
  sync ACTION_ID
  pause ACTION_ID
  resume ACTION_ID
  approve-deletes [--yes --digest D] ACTION_ID
              (lists the pending deletions and a review digest; --yes --digest D approves them only if they are still exactly the ones you reviewed)
  snapshot ACTION_ID
  snapshots ACTION_ID
  restore --to local|cloud|both [--path P]... ACTION_ID TS
  conflicts ACTION_ID
  diff ACTION_ID PATH
  resolve (--all local|cloud | --path P=local|cloud ...) ACTION_ID
  history [--limit N] ACTION_ID

Environment: S3_ANYSYNC_DATA_DIR overrides the data folder.
`

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

type cli struct {
	svc api.Service
	out io.Writer
}

type command func(*cli, context.Context, []string) error

func run(args []string, stdout, stderr io.Writer, connect connector) int {
	commands := map[string]command{
		"status": (*cli).status, "machine": (*cli).machine, "settings": (*cli).settings, "autostart": (*cli).autostart,
		"profile": (*cli).profile, "action": (*cli).action, "item": (*cli).item,
		"sync": (*cli).sync, "pause": (*cli).pause, "resume": (*cli).resume, "approve-deletes": (*cli).approveDeletes,
		"snapshot": (*cli).snapshot, "snapshots": (*cli).snapshots, "restore": (*cli).restore,
		"conflicts": (*cli).conflicts, "diff": (*cli).diff, "resolve": (*cli).resolve, "history": (*cli).history,
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
	ctx := context.Background()
	svc, closeFn, err := connect(ctx)
	if err != nil {
		reportError(stderr, err)
		return 1
	}
	defer closeFn()
	if err := cmd(&cli{svc: svc, out: stdout}, ctx, args[1:]); err != nil {
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
		return "  hint: the keycheck file (.meta/keycheck) of this action was lost in the bucket, so the passphrase cannot be verified; restore that object from a backup of the bucket or ask the machine that created the action."
	case errors.Is(err, engine.ErrDecrypt):
		return "  hint: the passphrase is wrong or the cloud data is damaged; check S3_ANYSYNC_PASSPHRASE."
	case errors.Is(err, engine.ErrPassphraseMissing):
		return "  hint: this action is encrypted; set S3_ANYSYNC_PASSPHRASE and re-run the same command (action create / action join / sync)."
	case errors.Is(err, engine.ErrRootMissing):
		return "  hint: an item's folder is missing (unmounted drive or renamed folder?); restore or re-mount it. If it was removed on purpose, unmap the item and map it again."
	case errors.Is(err, storage.ErrLocked):
		return "  hint: another machine is syncing this action; try again in a minute."
	case errors.Is(err, storage.ErrLockLost):
		return "  hint: this machine lost the cloud lock during the run (it took too long or the connection dropped); nothing is corrupted, run the sync again."
	case errors.Is(err, datalock.ErrBusy):
		return "  hint: another s3-anysync process owns the data folder; if the agent is starting or stopping, try again in a moment."
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

func when(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (c *cli) status(ctx context.Context, args []string) error {
	if _, err := parseArgs(flag.NewFlagSet("status", flag.ContinueOnError), args); err != nil {
		return err
	}
	st, err := c.svc.Status(ctx)
	if err != nil {
		return err
	}
	served := "in this process (the agent is not running)"
	if st.Agent {
		served = "by the background agent " + st.Version
	}
	fmt.Fprintf(c.out, "machine: %s (%s)\nserved: %s\n", st.MachineName, st.MachineID, served)
	w := c.table()
	fmt.Fprintln(w, "ID\tNAME\tSTATE\tLAST RUN\tNEXT RUN\tDETAILS")
	for _, a := range st.Actions {
		state := a.State
		if a.Running {
			state += " (running)"
		}
		details := a.LastSummary
		if a.LastError != "" {
			details = "error: " + a.LastError
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", a.Action.ID, a.Action.Name, state, when(a.LastRun), when(a.NextRun), details)
	}
	return w.Flush()
}

func (c *cli) machine(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("machine", flag.ContinueOnError)
	name := fs.String("name", "", "new friendly name")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	s, err := c.svc.Settings(ctx)
	if err != nil {
		return err
	}
	if *name != "" {
		s.MachineName = *name
		if s, err = c.svc.UpdateSettings(ctx, s); err != nil {
			return err
		}
	}
	fmt.Fprintf(c.out, "machine id: %s\nmachine name: %s\n", s.MachineID, s.MachineName)
	return nil
}

func (c *cli) settings(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("settings", flag.ContinueOnError)
	level := fs.String("log-level", "", "debug, info, warn or error")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	s, err := c.svc.Settings(ctx)
	if err != nil {
		return err
	}
	if *level != "" {
		s.LogLevel = *level
		if s, err = c.svc.UpdateSettings(ctx, s); err != nil {
			return err
		}
	}
	auto := "not supported on this system"
	if s.AutostartSupported {
		auto = yesNo(s.Autostart)
	}
	fmt.Fprintf(c.out, "machine name: %s\nlog level: %s\nautostart: %s\n", s.MachineName, s.LogLevel, auto)
	return nil
}

func (c *cli) autostart(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("autostart: expected enable, disable or status")
	}
	s, err := c.svc.Settings(ctx)
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
	case "enable", "disable":
		s.Autostart = args[0] == "enable"
		if s, err = c.svc.UpdateSettings(ctx, s); err != nil {
			return err
		}
	default:
		return fmt.Errorf("autostart: unknown subcommand %q", args[0])
	}
	if !s.AutostartSupported {
		fmt.Fprintln(c.out, "autostart: not supported on this system")
		return nil
	}
	fmt.Fprintf(c.out, "autostart: %s\n", yesNo(s.Autostart))
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
		if p.AccessKeyID == "" {
			return errors.New("profile add: --access-key is required")
		}
		secret := os.Getenv("S3_ANYSYNC_SECRET")
		if secret == "" {
			return errors.New("set S3_ANYSYNC_SECRET to the secret access key")
		}
		p, err := c.svc.AddProfile(ctx, api.AddProfileRequest{Profile: p, Secret: secret})
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "profile created: %s\n", p.ID)
		return c.printProbe(ctx, p.ID)
	case "list":
		if _, err := parseArgs(flag.NewFlagSet("profile list", flag.ContinueOnError), args[1:]); err != nil {
			return err
		}
		profiles, err := c.svc.Profiles(ctx)
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
	res, err := c.svc.TestProfile(ctx, id)
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

func (c *cli) machineID(ctx context.Context) (string, error) {
	s, err := c.svc.Settings(ctx)
	return s.MachineID, err
}

func (c *cli) action(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("action: expected create, list, discover, join, direction, rules or leave")
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
		fs.IntVar(&a.SnapshotRetention, "retention", model.DefaultSnapshotRetention, "manual snapshots to keep")
		if _, err := parseArgs(fs, args[1:]); err != nil {
			return err
		}
		a.Direction = model.Direction(*dir)
		a.PropagateDeletes = !*noDel
		a.Excludes = excludes
		created, err := c.svc.CreateAction(ctx, api.CreateActionRequest{ProfileID: *profile, Action: a, Passphrase: os.Getenv("S3_ANYSYNC_PASSPHRASE")})
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "action created: %s\n", created.ID)
		return nil
	case "list":
		if _, err := parseArgs(flag.NewFlagSet("action list", flag.ContinueOnError), args[1:]); err != nil {
			return err
		}
		mid, err := c.machineID(ctx)
		if err != nil {
			return err
		}
		actions, err := c.svc.Actions(ctx)
		if err != nil {
			return err
		}
		w := c.table()
		fmt.Fprintln(w, "ID\tNAME\tDIRECTION (THIS MACHINE)\tITEMS\tENCRYPTED\tSTATE")
		for _, st := range actions {
			a := st.Action
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%v\t%s\n", a.ID, a.Name, a.DirectionFor(mid), len(a.Items), a.Encrypt, st.State)
		}
		return w.Flush()
	case "discover":
		fs := flag.NewFlagSet("action discover", flag.ContinueOnError)
		profile := fs.String("profile", "", "profile id")
		if _, err := parseArgs(fs, args[1:]); err != nil {
			return err
		}
		found, err := c.svc.Discover(ctx, *profile)
		if err != nil {
			return err
		}
		local, err := c.svc.Actions(ctx)
		if err != nil {
			return err
		}
		joined := map[string]bool{}
		for _, st := range local {
			joined[st.Action.ID] = true
		}
		w := c.table()
		fmt.Fprintln(w, "ID\tNAME\tITEMS\tJOINED")
		for _, a := range found {
			fmt.Fprintf(w, "%s\t%s\t%d\t%v\n", a.ID, a.Name, len(a.Items), joined[a.ID])
		}
		return w.Flush()
	case "join":
		fs := flag.NewFlagSet("action join", flag.ContinueOnError)
		profile := fs.String("profile", "", "profile id")
		pos, err := parseArgs(fs, args[1:], "ACTION_ID")
		if err != nil {
			return err
		}
		a, err := c.svc.JoinAction(ctx, api.JoinActionRequest{ProfileID: *profile, ActionID: pos[0], Passphrase: os.Getenv("S3_ANYSYNC_PASSPHRASE")})
		if err != nil {
			return err
		}
		mid, err := c.machineID(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "joined action: %s\n", a.ID)
		w := c.table()
		fmt.Fprintln(w, "ITEM_ID\tREMOTE NAME\tTYPE\tLOCAL PATH")
		for _, it := range a.Items {
			local := it.LocalPaths[mid]
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
		if err := c.svc.SetMachineDirection(ctx, pos[0], d); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "direction updated")
		return nil
	case "rules":
		return c.rules(ctx, args[1:])
	case "leave":
		fs := flag.NewFlagSet("action leave", flag.ContinueOnError)
		force := fs.Bool("force", false, "leave locally even if the bucket cannot be updated")
		pos, err := parseArgs(fs, args[1:], "ACTION_ID")
		if err != nil {
			return err
		}
		if err := c.svc.LeaveAction(ctx, pos[0], *force); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "left action: %s (local files and the cloud copy were kept)\n", pos[0])
		return nil
	}
	return fmt.Errorf("action: unknown subcommand %q", args[0])
}

func (c *cli) rules(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("action rules", flag.ContinueOnError)
	name := fs.String("name", "", "action name")
	dir := fs.String("direction", "", "default direction")
	onChange := fs.Bool("on-change", false, "sync when local files change")
	debounce := fs.Int("debounce", 0, "seconds to wait for changes to settle")
	sched := fs.String("schedule", "", "none | every:MINUTES | daily:HH:MM | weekly:DAY@HH:MM | cron:SPEC")
	poll := fs.Int("poll", 0, "minutes between cloud checks (on-change without schedule)")
	prop := fs.Bool("propagate-deletes", true, "copy deletions to the other side")
	recv := fs.Bool("receiver-restores-cloud", false, "cloud-to-local machines upload files the cloud deleted (when deletions are not copied)")
	var excludes multiFlag
	fs.Var(&excludes, "exclude", "exclude glob (repeatable; replaces the list)")
	noEx := fs.Bool("no-excludes", false, "clear the exclude list")
	ret := fs.Int("retention", 0, "manual snapshots to keep")
	gFiles := fs.Int("delete-guard-files", 0, "delete guard: minimum files")
	gPct := fs.Int("delete-guard-percent", 0, "delete guard: minimum percent of synced files")
	pos, err := parseArgs(fs, args, "ACTION_ID")
	if err != nil {
		return err
	}
	st, err := c.svc.Action(ctx, pos[0])
	if err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if len(set) == 0 {
		c.printRules(st.Action)
		return nil
	}
	if set["exclude"] && *noEx {
		return errors.New("action rules: use either --exclude or --no-excludes")
	}
	for _, v := range []struct {
		name string
		val  *int
	}{{"retention", ret}, {"delete-guard-files", gFiles}, {"debounce", debounce}, {"poll", poll}} {
		if set[v.name] && *v.val < 1 {
			return fmt.Errorf("--%s must be at least 1", v.name)
		}
	}
	if set["delete-guard-percent"] && (*gPct < 1 || *gPct > 100) {
		return errors.New("--delete-guard-percent must be between 1 and 100")
	}
	r := st.Action.Rules()
	if set["name"] {
		r.Name = *name
	}
	if set["direction"] {
		r.Direction = model.Direction(*dir)
	}
	if set["on-change"] {
		r.Triggers.OnChange = *onChange
	}
	if set["debounce"] {
		r.Triggers.DebounceSeconds = *debounce
	}
	if set["schedule"] {
		s, err := parseSchedule(*sched)
		if err != nil {
			return err
		}
		r.Triggers.Schedule = s
	}
	if set["poll"] {
		r.Triggers.RemotePollMinutes = *poll
	}
	if set["propagate-deletes"] {
		r.PropagateDeletes = *prop
	}
	if set["receiver-restores-cloud"] {
		r.ReceiverRestoresCloud = *recv
	}
	if set["exclude"] {
		r.Excludes = excludes
	}
	if *noEx {
		r.Excludes = nil
	}
	if set["retention"] {
		r.SnapshotRetention = *ret
	}
	if set["delete-guard-files"] {
		r.DeleteGuardMinFiles = *gFiles
	}
	if set["delete-guard-percent"] {
		r.DeleteGuardPercent = *gPct
	}
	a, err := c.svc.UpdateRules(ctx, pos[0], r)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, "rules updated")
	c.printRules(a)
	return nil
}

func (c *cli) printRules(a model.Action) {
	ex := "none"
	if len(a.Excludes) > 0 {
		ex = strings.Join(a.Excludes, ", ")
	}
	fmt.Fprintf(c.out, "name: %s\ndirection: %s\non change: %s (debounce %ds)\nschedule: %s\n",
		a.Name, a.Direction, yesNo(a.Triggers.OnChange), a.Triggers.DebounceSeconds, describeSchedule(a.Triggers.Schedule))
	if runs, err := schedule.NextRuns(a.Triggers.Schedule, time.Now(), 3); err == nil && len(runs) > 0 {
		var s []string
		for _, r := range runs {
			s = append(s, r.Format("2006-01-02 15:04"))
		}
		fmt.Fprintf(c.out, "next runs: %s\n", strings.Join(s, ", "))
	}
	fmt.Fprintf(c.out, "cloud check: every %d minute(s)\npropagate deletes: %s\nreceiver restores cloud: %s\nexcludes: %s\nmanual snapshots kept: %d\ndelete guard: %d file(s) and %d%%\n",
		a.Triggers.RemotePollMinutes, yesNo(a.PropagateDeletes), yesNo(a.ReceiverRestoresCloud), ex,
		a.SnapshotRetention, a.DeleteGuardMinFiles, a.DeleteGuardPercent)
}

func (c *cli) item(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("item: expected add, map, unmap or remove")
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
		if *typ != string(model.ItemFile) && *typ != string(model.ItemDir) {
			return errors.New("item add: --type must be file or dir")
		}
		it.Type = model.ItemType(*typ)
		it.Excludes = excludes
		added, err := c.svc.AddItem(ctx, pos[0], api.AddItemRequest{Item: it, LocalPath: *local})
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
		if err := c.svc.MapItem(ctx, pos[0], pos[1], *local); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "item mapped")
		return nil
	case "unmap":
		pos, err := parseArgs(flag.NewFlagSet("item unmap", flag.ContinueOnError), args[1:], "ACTION_ID", "ITEM_ID")
		if err != nil {
			return err
		}
		if err := c.svc.UnmapItem(ctx, pos[0], pos[1]); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "item unmapped on this machine (its files stay where they are)")
		return nil
	case "remove":
		pos, err := parseArgs(flag.NewFlagSet("item remove", flag.ContinueOnError), args[1:], "ACTION_ID", "ITEM_ID")
		if err != nil {
			return err
		}
		if err := c.svc.RemoveItem(ctx, pos[0], pos[1]); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "item removed from the action on every machine (files stay where they are)")
		return nil
	}
	return fmt.Errorf("item: unknown subcommand %q", args[0])
}

// actionArg parses a command whose only argument is ACTION_ID (plus extras).
func actionArg(fs *flag.FlagSet, args []string, extra ...string) ([]string, error) {
	return parseArgs(fs, args, append([]string{"ACTION_ID"}, extra...)...)
}

func (c *cli) printResult(res model.SyncResult) {
	fmt.Fprintf(c.out, "%s: %s\n", res.Outcome, res.Summary())
	for _, cf := range res.Conflicts {
		fmt.Fprintf(c.out, "  conflict: %s\n", cf.Path)
	}
	for _, p := range res.Deferred {
		fmt.Fprintf(c.out, "  deferred (changed during sync, will be re-planned): %s\n", p)
	}
	for _, p := range res.PendingDeletes {
		fmt.Fprintf(c.out, "  pending deletion: %s\n", p)
	}
	if res.Outcome == model.OutcomeDeletesPending {
		fmt.Fprintln(c.out, "These deletions are held back by the delete guard. Check them (approve-deletes ACTION_ID), then approve with the printed --yes --digest command (or restore the files).")
	}
	for _, p := range res.Unsupported {
		fmt.Fprintf(c.out, "  not synced here (name not allowed on this system): %s\n", p)
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
	pos, err := actionArg(flag.NewFlagSet("sync", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	res, err := c.svc.Sync(ctx, pos[0])
	if err != nil {
		return err
	}
	c.printResult(res)
	return nil
}

func (c *cli) pause(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("pause", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if err := c.svc.Pause(ctx, pos[0]); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "paused (automatic syncs stop until resume; manual commands still work)")
	return nil
}

func (c *cli) resume(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("resume", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if err := c.svc.Resume(ctx, pos[0]); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "resumed")
	return nil
}

// reviewDigest identifies a set of pending deletions: the first 8 hex chars
// of the SHA-256 of the sorted paths joined by newlines.
func reviewDigest(paths []string) string {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:])[:8]
}

func (c *cli) approveDeletes(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("approve-deletes", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "approve the deletions identified by --digest")
	digest := fs.String("digest", "", "review digest printed by the listing")
	pos, err := actionArg(fs, args)
	if err != nil {
		return err
	}
	if *yes && *digest == "" {
		return errors.New("approve-deletes: --yes needs --digest from the listing (so you approve exactly what you reviewed)")
	}
	p, err := c.svc.PendingDeletes(ctx, pos[0])
	if err != nil {
		return err
	}
	if p == nil {
		fmt.Fprintln(c.out, "no deletions are waiting for approval")
		return nil
	}
	cur := reviewDigest(p.Paths)
	list := func() {
		for _, x := range p.Paths {
			fmt.Fprintf(c.out, "  pending deletion: %s\n", x)
		}
		fmt.Fprintf(c.out, "review digest: %s\n", cur)
	}
	if !*yes {
		list()
		fmt.Fprintf(c.out, "To approve exactly these %d deletion(s): approve-deletes --yes --digest %s %s\n", len(p.Paths), cur, pos[0])
		return nil
	}
	if cur != *digest {
		list()
		fmt.Fprintln(c.out, "The pending deletions changed since you reviewed them; nothing was approved. Review the list above and use the new digest.")
		return errors.New("approve-deletes: the pending deletions changed (digest mismatch)")
	}
	res, err := c.svc.ApproveDeletes(ctx, pos[0], p.Paths)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "approved %d deletion(s)\n", len(p.Paths))
	c.printResult(res)
	return nil
}

func (c *cli) snapshot(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("snapshot", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	info, err := c.svc.Snapshot(ctx, pos[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "snapshot taken: %s (%d files)\n", info.TS, len(info.Files))
	return nil
}

// machines returns the action's machine names (for display).
func (c *cli) machines(ctx context.Context, id string) map[string]string {
	st, err := c.svc.Action(ctx, id)
	if err != nil {
		return nil
	}
	return st.Action.Machines
}

func (c *cli) snapshots(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("snapshots", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	list, err := c.svc.Snapshots(ctx, pos[0])
	if err != nil {
		return err
	}
	machines := c.machines(ctx, pos[0])
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
	pos, err := actionArg(fs, args, "TS")
	if err != nil {
		return err
	}
	target := engine.RestoreTarget(*to)
	if target != engine.RestoreLocal && target != engine.RestoreCloud && target != engine.RestoreBoth {
		return fmt.Errorf("restore: --to must be local, cloud or both (got %q)", *to)
	}
	res, err := c.svc.Restore(ctx, pos[0], api.RestoreRequest{TS: pos[1], Paths: only, To: target})
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "restored %d file(s)\n", len(res.Restored))
	if target == engine.RestoreLocal {
		fmt.Fprintln(c.out, "note: restored locally only; a two-way machine will upload these files again at the next sync, and a cloud-to-local machine will re-download the cloud version.")
	}
	for _, p := range res.SkippedUnmapped {
		fmt.Fprintf(c.out, "  skipped (not mapped on this machine): %s\n", p)
	}
	return nil
}

func (c *cli) conflicts(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("conflicts", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	list, err := c.svc.Conflicts(ctx, pos[0])
	if err != nil {
		return err
	}
	machines := c.machines(ctx, pos[0])
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

func (c *cli) diff(ctx context.Context, args []string) error {
	pos, err := actionArg(flag.NewFlagSet("diff", flag.ContinueOnError), args, "PATH")
	if err != nil {
		return err
	}
	d, err := c.svc.ConflictDetail(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	switch {
	case d.TooLarge:
		fmt.Fprintln(c.out, "a side is larger than 1 MiB; no text shown")
	case d.Binary:
		fmt.Fprintln(c.out, "binary content; no text shown")
	default:
		fmt.Fprintf(c.out, "--- this machine (%s)\n%s\n+++ cloud (%s)\n%s\n", describe(d.Conflict.Local), d.LocalText, describe(d.Conflict.Remote), d.RemoteText)
	}
	return nil
}

func (c *cli) resolve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	all := fs.String("all", "", "resolve every conflict: local or cloud")
	var each multiFlag
	fs.Var(&each, "path", "PATH=local|cloud (repeatable)")
	pos, err := actionArg(fs, args)
	if err != nil {
		return err
	}
	choices := map[string]model.Side{}
	if *all != "" {
		list, err := c.svc.Conflicts(ctx, pos[0])
		if err != nil {
			return err
		}
		for _, cf := range list {
			choices[cf.Path] = model.Side(*all)
		}
	}
	for _, kv := range each {
		p, side, ok := cutLast(kv)
		if !ok {
			return fmt.Errorf("--path %q: expected PATH=local or PATH=cloud", kv)
		}
		choices[p] = model.Side(side)
	}
	if *all == "" && len(each) == 0 {
		return errors.New("nothing to resolve: pass --all local|cloud or --path PATH=local|cloud")
	}
	res, err := c.svc.Resolve(ctx, pos[0], choices)
	if err != nil {
		return err
	}
	c.printResult(res)
	if res.Outcome == model.OutcomeConflict {
		c.printConflictHint(res.Conflicts)
	}
	return nil
}

func (c *cli) history(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "entries to show")
	pos, err := actionArg(fs, args)
	if err != nil {
		return err
	}
	list, err := c.svc.History(ctx, pos[0], *limit)
	if err != nil {
		return err
	}
	w := c.table()
	fmt.Fprintln(w, "STARTED\tKIND\tOUTCOME\tDETAILS")
	for _, h := range list {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", when(h.StartedAt), h.Kind, h.Outcome, h.Message)
	}
	return w.Flush()
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

// cutLast splits s at the last '=' so paths may contain '='.
func cutLast(s string) (before, after string, ok bool) {
	i := strings.LastIndex(s, "=")
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}
