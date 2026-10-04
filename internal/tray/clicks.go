package tray

// clickHandlers are invoked by serveClicks, one per click.
type clickHandlers struct {
	Sync, Snapshot, Pause func()
}

// serveClicks dispatches clicks until stop is closed or any click channel is
// closed (systray closes ClickedCh when an item is removed). stop is checked
// first on every iteration so a removed action never triggers a handler.
func serveClicks(stop <-chan struct{}, sync, snap, pause <-chan struct{}, h clickHandlers) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		select {
		case <-stop:
			return
		case _, ok := <-sync:
			if !ok {
				return
			}
			if !stopped(stop) {
				h.Sync()
			}
		case _, ok := <-snap:
			if !ok {
				return
			}
			if !stopped(stop) {
				h.Snapshot()
			}
		case _, ok := <-pause:
			if !ok {
				return
			}
			if !stopped(stop) {
				h.Pause()
			}
		}
	}
}

func stopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}
