//go:build cgo || windows

package main

import (
	"context"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// wailsRuntime adapts the Wails runtime to gui.Dialogs and event emission.
type wailsRuntime struct {
	mu  sync.Mutex
	ctx context.Context
}

func (r *wailsRuntime) setContext(c context.Context) { r.mu.Lock(); r.ctx = c; r.mu.Unlock() }

func (r *wailsRuntime) get() context.Context { r.mu.Lock(); defer r.mu.Unlock(); return r.ctx }

func (r *wailsRuntime) emit(name string, data any) {
	if c := r.get(); c != nil {
		wruntime.EventsEmit(c, name, data)
	}
}

func (r *wailsRuntime) PickFile(title string) (string, error) {
	return wruntime.OpenFileDialog(r.get(), wruntime.OpenDialogOptions{Title: title, ShowHiddenFiles: true})
}

func (r *wailsRuntime) PickFolder(title string) (string, error) {
	return wruntime.OpenDirectoryDialog(r.get(), wruntime.OpenDialogOptions{Title: title, ShowHiddenFiles: true, CanCreateDirectories: true})
}

func (r *wailsRuntime) focus() {
	if c := r.get(); c != nil {
		wruntime.WindowUnminimise(c)
		wruntime.WindowShow(c)
	}
}
