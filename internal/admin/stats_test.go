package admin

import (
	"testing"

	"github.com/ggmolly/belfast/internal/game"
)

func TestItemDeltas(t *testing.T) {
	baseline := map[uint32]uint32{1: 10, 2: 5, 3: 7}
	current := map[uint32]uint32{1: 4, 2: 5, 4: 3}
	got := itemDeltas(baseline, current, 0)
	want := []itemDelta{{ID: 3, Delta: -7}, {ID: 1, Delta: -6}, {ID: 4, Delta: 3}}
	if len(got) != len(want) {
		t.Fatalf("got %d deltas, want %d: %v", len(got), len(want), got)
	}
	for index := range want {
		if got[index].ID != want[index].ID || got[index].Delta != want[index].Delta {
			t.Fatalf("delta %d = %v, want %v", index, got[index], want[index])
		}
	}
}

func TestItemDeltasSkipsUnchangedAndLimits(t *testing.T) {
	baseline := map[uint32]uint32{1: 10, 2: 5}
	current := map[uint32]uint32{1: 10, 2: 8}
	got := itemDeltas(baseline, current, 1)
	if len(got) != 1 || got[0].ID != 2 || got[0].Delta != 3 {
		t.Fatalf("got %v, want only item 2 at +3", got)
	}
	if len(itemDeltas(nil, nil, 0)) != 0 {
		t.Fatalf("empty snapshots should have no deltas")
	}
}

func TestCountsOf(t *testing.T) {
	counts := countsOf(gameSnapshot(resourceOil, 100, resourceGold, 20, resourceGem, 3, resourceFreeGem, 4))
	if counts.Oil != 100 || counts.Gold != 20 || counts.Gems != 7 {
		t.Fatalf("counts = %+v, want oil 100 gold 20 gems 7", counts)
	}
}

// gameSnapshot builds the four resources the summary shows.
func gameSnapshot(pairs ...uint32) game.SessionBaseline {
	snapshot := game.SessionBaseline{Resources: map[uint32]uint32{}}
	for index := 0; index+1 < len(pairs); index += 2 {
		snapshot.Resources[pairs[index]] = pairs[index+1]
	}
	return snapshot
}
