// Package tray shows the agent's status icon and menu. The real tray needs
// CGO on macOS; builds without it get a no-op stub.
package tray

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/aupr/s3-anysync/internal/api"
)

// Options configure the tray.
type Options struct {
	// OnQuit is called when the user picks Quit.
	OnQuit func()
	// OpenGUI opens the settings window; nil hides the Open item.
	OpenGUI func() error
	Log     *slog.Logger
}

type iconKind int

const (
	iconIdle iconKind = iota
	iconSyncing
	iconAttention
	iconError
)

// iconFor picks the icon: error beats attention beats syncing beats idle.
func iconFor(st api.Status) iconKind {
	kind := iconIdle
	for _, a := range st.Actions {
		switch {
		case a.State == api.StateError:
			return iconError
		case a.State == api.StatePausedConflict || a.State == api.StatePausedDeletes:
			kind = iconAttention
		case a.Running && kind == iconIdle:
			kind = iconSyncing
		}
	}
	return kind
}

const maxTitle = 80

// actionTitle is the menu line for one action.
func actionTitle(a api.ActionStatus) string {
	var s string
	switch {
	case a.Running:
		s = "syncing…"
	case a.State == api.StatePausedConflict:
		s = fmt.Sprintf("%d conflict(s)", a.Conflicts)
	case a.State == api.StatePausedDeletes:
		s = fmt.Sprintf("%d deletion(s) to approve", a.PendingDeletes)
	case a.State == api.StatePausedUser:
		s = "paused"
	case a.State == api.StateError:
		s = "error (" + cleanLine(a.LastError) + ")"
	default:
		s = "idle"
	}
	title := []rune(a.Action.Name + ": " + s)
	if len(title) > maxTitle {
		title = append(title[:maxTitle-1], '…')
	}
	return string(title)
}

// tooltip summarises all actions.
func tooltip(st api.Status) string {
	if len(st.Actions) == 0 {
		return "s3-anysync: no actions"
	}
	attention := 0
	for _, a := range st.Actions {
		switch a.State {
		case api.StateError, api.StatePausedConflict, api.StatePausedDeletes:
			attention++
		}
	}
	if attention == 0 {
		return fmt.Sprintf("s3-anysync: %d action(s), all fine", len(st.Actions))
	}
	return fmt.Sprintf("s3-anysync: %d action(s), %d need attention", len(st.Actions), attention)
}

// cleanLine replaces newlines and other control characters with spaces so
// the text fits a single menu line.
func cleanLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
