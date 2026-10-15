package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/irbis-sh/zen-desktop/internal/filterliststore"
)

// The background subscription refresher keeps filter-list caches current
// without ever touching the live filter or delaying the proxy start
// (AdGuard-Home-style model, cf. AdGuardHome internal/filtering/filter.go:
// per-filter LastUpdated + FiltersUpdateIntervalHours, force flag for manual
// updates). The startup build serves whatever copy is cached
// (app/filter.go passModes); refreshed content lands in the disk cache and is
// picked up by the next build.

const (
	// defaultFilterUpdateInterval matches the scheduler default: one cycle
	// per hour. A cycle skips fresh lists without any network traffic, so the
	// interval only bounds how long an expired list may go unnoticed.
	defaultFilterUpdateInterval = time.Hour

	// backgroundRefreshDelay spaces the first cycle out from the startup
	// build, which has just finished parsing every cached list.
	backgroundRefreshDelay = 45 * time.Second

	// refreshCycleBudget bounds one refresh cycle: the store's stall watchdog
	// and stale fallbacks bound individual fetches, so the budget only caps
	// the pathological tail (a blackholed mirror on every list).
	refreshCycleBudget = 15 * time.Minute
)

// filterUpdateManualInFlight guards the manual force refresh against
// double-firing (the frontend disables its button, but bindings are also
// callable directly).
var filterUpdateManualInFlight atomic.Bool

// startFilterUpdateScheduler runs one refresh cycle backgroundRefreshDelay
// after startup and then once per configured interval. Cycles only mutate
// the disk cache - the live filter is never touched, so no restart is
// triggered and refreshed rules apply at the next proxy start.
func (a *App) startFilterUpdateScheduler() {
	interval := time.Duration(a.config.GetFilterUpdateIntervalHours()) * time.Hour
	if interval <= 0 {
		interval = defaultFilterUpdateInterval
	}
	go func() {
		timer := time.NewTimer(backgroundRefreshDelay)
		defer timer.Stop()
		for {
			<-timer.C
			ctx, cancel := context.WithTimeout(context.Background(), refreshCycleBudget)
			a.refreshFilterListsCache(ctx, false)
			cancel()
			timer.Reset(interval)
		}
	}()
}

// refreshFilterListsCache re-downloads expired filter lists (all of them,
// ignoring freshness, when force is set) into the disk cache, without
// touching the live filter. It reports whether any list actually changed.
func (a *App) refreshFilterListsCache(ctx context.Context, force bool) bool {
	mode := filterliststore.ModeDefault
	if force {
		mode = filterliststore.ModeForceFetch
	}

	lists := a.config.GetFilterLists()
	var wg sync.WaitGroup
	var mu sync.Mutex
	changed := false
	for _, filterList := range lists {
		if !filterList.Enabled {
			continue
		}
		wg.Add(1)
		go func(url, name string) {
			defer wg.Done()
			rc, src, err := a.filterListStore.Get(ctx, url, mode)
			if err != nil {
				log.Printf("filter update: %q (%s): %v", url, name, err)
				return
			}
			switch src {
			case filterliststore.SourceNetwork:
				// Drain to a verified EOF so the download is promoted into
				// the cache; a broken body promotes nothing.
				if _, copyErr := io.Copy(io.Discard, rc); copyErr != nil {
					log.Printf("filter update: %q (%s): download broke mid-body: %v", url, name, copyErr)
				} else {
					mu.Lock()
					changed = true
					mu.Unlock()
					log.Printf("filter update: %q (%s): downloaded fresh copy", url, name)
				}
			case filterliststore.SourceCache:
				log.Printf("filter update: %q (%s): up to date", url, name)
			case filterliststore.SourceStaleCache:
				log.Printf("filter update: %q (%s): network failed, stale copy kept", url, name)
			}
			rc.Close()
		}(filterList.URL, filterList.Name)
	}
	wg.Wait()
	return changed
}

// ForceRefreshFilterLists re-downloads every enabled subscription ignoring
// cache freshness (a 304 still counts as unchanged) and, when the proxy is
// running and something changed, restarts it so the new rules apply
// immediately. Called from the frontend filter-lists page.
func (a *App) ForceRefreshFilterLists() (bool, error) {
	if !filterUpdateManualInFlight.CompareAndSwap(false, true) {
		return false, errors.New("a filter update is already in progress")
	}
	defer filterUpdateManualInFlight.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), refreshCycleBudget)
	defer cancel()
	changed := a.refreshFilterListsCache(ctx, true)
	if !changed {
		log.Println("filter update: manual refresh found every list up to date")
		return false, nil
	}

	a.proxyMu.Lock()
	on := a.proxyOn
	a.proxyMu.Unlock()
	if !on {
		log.Println("filter update: lists refreshed, proxy is off so they apply at the next start")
		return true, nil
	}

	log.Println("filter update: restarting the proxy to apply refreshed lists")
	// Residual race: a user stop landing between this restart's stop and
	// start would be overridden. The window is one stop/start cycle; both
	// operations serialize on proxyMu.
	if err := a.StopProxy(); err != nil {
		return true, fmt.Errorf("apply refreshed lists: %w", err)
	}
	if err := a.StartProxy(); err != nil {
		return true, fmt.Errorf("apply refreshed lists: %w", err)
	}
	return true, nil
}
