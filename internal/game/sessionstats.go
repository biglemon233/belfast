package game

import (
	"context"
	"sync"
	"time"
)

// Where a comparison window came from, as the back-office reports it.
const (
	// BaselineSourceLogin is a window opened by the client's login.
	BaselineSourceLogin = "login"
	// BaselineSourceOnDemand is a window the back-office had to open itself,
	// for a commander the server did not see log in.
	BaselineSourceOnDemand = "on_demand"
)

// SessionBaseline is a commander's inventory at the moment it logged in. The
// back-office diffs the current rows against it to answer "what changed since
// this login", so no history table has to be kept.
type SessionBaseline struct {
	// At is when the window was opened (the login, or the request that had
	// to open it on demand).
	At time.Time
	// Source is BaselineSourceLogin or BaselineSourceOnDemand.
	Source string
	// Resources maps player_resource ids (gold, oil, gems) to amounts.
	Resources map[uint32]uint32
	// Items maps item ids to counts.
	Items map[uint32]uint32
	// Ships is how many ships the dock held.
	Ships int64
	// Sorties is the sum of chapter_progress.today_defeat_count: the game
	// already counts each map's sorties per day, so the window can show how
	// many were fought after the login without recording anything.
	Sorties int64
}

// sessionBaselines holds one snapshot per commander, replaced when a new
// comparison window opens: the day's first login does, a relogin the same day
// keeps the window (a client restarting mid-session must not reset it).
// Snapshots survive a logout so the back-office can still show the window
// that just closed.
type sessionBaselines struct {
	mu   sync.Mutex
	byID map[uint32]SessionBaseline
}

// InventorySnapshot reads what a comparison window compares: resources,
// items, the dock's size and the day's sortie counter.
func (game *Game) InventorySnapshot(ctx context.Context, id uint32) (SessionBaseline, error) {
	snapshot := SessionBaseline{
		At:        time.Now(),
		Resources: map[uint32]uint32{},
		Items:     map[uint32]uint32{},
	}
	resources, err := game.DB.ListResources(ctx, id)
	if err != nil {
		return SessionBaseline{}, err
	}
	for _, row := range resources {
		snapshot.Resources[row.ResourceID] = row.Amount
	}
	items, err := game.DB.ListItems(ctx, id)
	if err != nil {
		return SessionBaseline{}, err
	}
	for _, row := range items {
		snapshot.Items[row.ItemID] = row.Count
	}
	if snapshot.Ships, err = game.DB.CountShipsByCommander(ctx, id); err != nil {
		return SessionBaseline{}, err
	}
	progress, err := game.DB.ListChapterProgress(ctx, id)
	if err != nil {
		return SessionBaseline{}, err
	}
	for _, row := range progress {
		snapshot.Sorties += int64(row.TodayDefeatCount)
	}
	return snapshot, nil
}

// CaptureSessionBaseline opens the day's comparison window on login: the
// first login of the day takes the snapshot, a later relogin keeps it.
func (game *Game) CaptureSessionBaseline(ctx context.Context, id uint32) error {
	return game.captureSessionBaseline(ctx, id, BaselineSourceLogin, true)
}

// CaptureSessionBaselineFrom opens a window the server did not see a login
// for, replacing whatever the commander had: the back-office's on-demand
// path, where the request itself is the start of the window.
func (game *Game) CaptureSessionBaselineFrom(ctx context.Context, id uint32, source string) error {
	return game.captureSessionBaseline(ctx, id, source, false)
}

// captureSessionBaseline takes the snapshot unless keepSameDay finds one from
// the same calendar day, in which case the open window stays untouched.
func (game *Game) captureSessionBaseline(ctx context.Context, id uint32, source string, keepSameDay bool) error {
	if keepSameDay {
		game.sessions.mu.Lock()
		existing, ok := game.sessions.byID[id]
		game.sessions.mu.Unlock()
		if ok && sameDay(existing.At, time.Now()) {
			return nil
		}
	}
	snapshot, err := game.InventorySnapshot(ctx, id)
	if err != nil {
		return err
	}
	snapshot.Source = source
	game.sessions.mu.Lock()
	defer game.sessions.mu.Unlock()
	if game.sessions.byID == nil {
		game.sessions.byID = make(map[uint32]SessionBaseline)
	}
	game.sessions.byID[id] = snapshot
	return nil
}

// SessionBaseline returns the window's snapshot, if one is open.
func (game *Game) SessionBaseline(id uint32) (SessionBaseline, bool) {
	game.sessions.mu.Lock()
	defer game.sessions.mu.Unlock()
	baseline, ok := game.sessions.byID[id]
	return baseline, ok
}

// sameDay tells whether two instants fall on the same local calendar day, the
// window the game's today_* counters reset on.
func sameDay(a, b time.Time) bool {
	aYear, aMonth, aDay := a.Date()
	bYear, bMonth, bDay := b.Date()
	return aYear == bYear && aMonth == bMonth && aDay == bDay
}
