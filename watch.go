package shikumi

import (
	"context"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// debounce coalesces bursty filesystem events into one reload.
const debounce = 150 * time.Millisecond

type fileWatcher struct {
	w      *fsnotify.Watcher
	cancel context.CancelFunc
}

// Watch reloads the store when its config file changes, calling onReload after
// each reload attempt. Per the keep-last-good contract, onReload always
// receives the CURRENT-GOOD config pointer (never a half-applied or invalid
// one): on a successful reload that is the new config; on a failed reload
// (malformed file or failed validation) it is the previously published value,
// alongside the non-nil reloadErr describing why the new value was rejected.
//
// It is symlink-aware: nix-darwin rewrites configs as store symlinks and swaps
// the target atomically (unlink + symlink) on rebuild, so the inode changes
// while the path stays put. We therefore watch the parent directory for events
// touching the file's name — Write/Create/Rename — and ignore bare Removes
// (the transient half of an atomic swap). Watching stops on ctx cancellation
// or Close.
func (s *Store[T]) Watch(ctx context.Context, onReload func(*T, error)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	base := filepath.Base(s.path)
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	s.watcher = &fileWatcher{w: w, cancel: cancel}

	go func() {
		defer w.Close()
		var timer *time.Timer
		fire := func() {
			// reloadCtx keeps last-good on failure, so s.Get() is always the
			// current-good pointer regardless of err.
			err := s.reloadCtx(ctx)
			if onReload != nil {
				onReload(s.Get(), err)
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Base(ev.Name) != base {
					continue
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(debounce, fire)
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return nil
}

// Close stops watching, if a Watch is active. Safe to call multiple times.
func (s *Store[T]) Close() error {
	if s.watcher != nil {
		s.watcher.cancel()
		s.watcher = nil
	}
	return nil
}
