package admin

import (
	"net/http"
	"sort"

	"github.com/ggmolly/belfast/internal/game"
)

// sessionCounts is one side of the comparison: what the commander held.
type sessionCounts struct {
	Oil     uint32 `json:"oil"`
	Gold    uint32 `json:"gold"`
	Gems    uint32 `json:"gems"`
	Ships   int64  `json:"ships"`
	Items   int    `json:"items"`
	Sorties int64  `json:"sorties"`
}

// sessionDelta is current minus baseline: negative means the window spent
// more than it earned.
type sessionDelta struct {
	Oil     int64 `json:"oil"`
	Gold    int64 `json:"gold"`
	Gems    int64 `json:"gems"`
	Ships   int64 `json:"ships"`
	Items   int   `json:"items"`
	Sorties int64 `json:"sorties"`
}

// itemDelta is one item's change over the window.
type itemDelta struct {
	ID    uint32 `json:"id"`
	Name  string `json:"name,omitempty"`
	Delta int64  `json:"delta"`
}

// sessionView is GET /api/commanders/{id}/session: the inventory the
// commander logged in with, the current one, and the difference.
type sessionView struct {
	Commander      uint32        `json:"commander"`
	Name           string        `json:"name"`
	Online         bool          `json:"online"`
	Since          int64         `json:"since"`
	BaselineSource string        `json:"baseline_source"`
	Baseline       sessionCounts `json:"baseline"`
	Current        sessionCounts `json:"current"`
	Delta          sessionDelta  `json:"delta"`
	Items          []itemDelta   `json:"items"`
}

// countsOf reads the amounts the summary shows out of a snapshot. Gems are the
// paid and free rows together, as Player:getTotalGem reports them.
func countsOf(snapshot game.SessionBaseline) sessionCounts {
	return sessionCounts{
		Oil:     snapshot.Resources[resourceOil],
		Gold:    snapshot.Resources[resourceGold],
		Gems:    snapshot.Resources[resourceGem] + snapshot.Resources[resourceFreeGem],
		Ships:   snapshot.Ships,
		Items:   len(snapshot.Items),
		Sorties: snapshot.Sorties,
	}
}

// itemDeltas diffs two item maps, biggest absolute change first; a limit of 0
// keeps them all. Items whose count did not move are left out.
func itemDeltas(baseline, current map[uint32]uint32, limit int) []itemDelta {
	seen := make(map[uint32]struct{}, len(baseline)+len(current))
	for id := range baseline {
		seen[id] = struct{}{}
	}
	for id := range current {
		seen[id] = struct{}{}
	}
	deltas := make([]itemDelta, 0, len(seen))
	for id := range seen {
		delta := int64(current[id]) - int64(baseline[id])
		if delta == 0 {
			continue
		}
		deltas = append(deltas, itemDelta{ID: id, Delta: delta})
	}
	sort.Slice(deltas, func(i, j int) bool {
		if absDelta(deltas[i].Delta) == absDelta(deltas[j].Delta) {
			return deltas[i].ID < deltas[j].ID
		}
		return absDelta(deltas[i].Delta) > absDelta(deltas[j].Delta)
	})
	if limit > 0 && len(deltas) > limit {
		deltas = deltas[:limit]
	}
	return deltas
}

// absDelta is math.Abs for the int64 deltas, kept local so the comparison
// above reads the same on both sides.
func absDelta(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

// handleSessionStats answers the back-office's "since this login" panel: it
// diffs the inventory the commander logged in with against the current rows.
// There is no history table: the baseline lives in memory and a new login
// replaces it on every connection. A commander the server saw connect before
// this process started (it did not: connections die with the process) or
// whose capture failed gets its window opened on this first request instead.
func (server *Server) handleSessionStats(r *http.Request) (any, error) {
	commander, err := server.commander(r)
	if err != nil {
		return nil, err
	}
	ctx, id := r.Context(), commander.ID
	baseline, ok := server.Game.SessionBaseline(id)
	if !ok {
		if err := server.Game.CaptureSessionBaselineFrom(ctx, id, game.BaselineSourceOnDemand); err != nil {
			return nil, err
		}
		baseline, _ = server.Game.SessionBaseline(id)
	}
	current, err := server.Game.InventorySnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	before, after := countsOf(baseline), countsOf(current)
	changed := itemDeltas(baseline.Items, current.Items, 0)
	items := changed
	if len(items) > 20 {
		items = items[:20]
	}
	for index := range items {
		items[index].Name = server.names[dropItem][items[index].ID].Name
	}
	return sessionView{
		Commander:      id,
		Name:           commander.Name,
		Online:         server.Game.IsOnline(id),
		Since:          baseline.At.Unix(),
		BaselineSource: baseline.Source,
		Baseline:       before,
		Current:        after,
		Delta: sessionDelta{
			Oil:     int64(after.Oil) - int64(before.Oil),
			Gold:    int64(after.Gold) - int64(before.Gold),
			Gems:    int64(after.Gems) - int64(before.Gems),
			Ships:   after.Ships - before.Ships,
			Items:   len(changed),
			Sorties: after.Sorties - before.Sorties,
		},
		Items: items,
	}, nil
}
