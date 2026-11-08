package game

import (
	"testing"

	"github.com/ggmolly/belfast/internal/region"
)

// TestChapterBattleDrops checks a won battle's loot comes from the enemy's own
// award_display (4-3): a normal enemy drops a design or a gear part besides
// the coins and sometimes a shipgirl, the boss adds the chapter's ship every
// time.
func TestChapterBattleDrops(t *testing.T) {
	t.Setenv("BELFAST_DATA", "../../data")
	if err := region.Set("CN"); err != nil {
		t.Fatalf("region: %v", err)
	}
	if err := loadChapterData(); err != nil {
		t.Fatalf("chapter data: %v", err)
	}
	template := chapterTemplates[403]
	if template == nil {
		t.Fatal("chapter 403 missing")
	}
	normal, ok := expeditionTemplates[403010]
	if !ok {
		t.Fatal("expedition 403010 missing")
	}
	items, ships := normal.pools()
	if len(items) == 0 || len(ships) != 0 {
		t.Fatalf("403010 pools: %d items, %d ships", len(items), len(ships))
	}
	for _, ref := range items {
		if len(dropPools[ref.id]) == 0 && itemNameTypes[ref.id].Type == 0 {
			t.Fatalf("%d is neither a known item nor a box", ref.id)
		}
	}
	// run it a few times: the coins and the item are always there, the ship
	// is a 45% roll
	for range 20 {
		drops := scenarioDrops(template, normal)
		if len(drops) < 2 || drops[0].GetType() != dropTypeResource {
			t.Fatalf("unexpected drops %v", drops)
		}
		for _, drop := range drops[1:] {
			if drop.GetType() == dropTypeResource {
				t.Fatalf("only the coins are a resource drop: %v", drops)
			}
		}
	}

	boss, ok := expeditionTemplates[403000]
	if !ok {
		t.Fatal("expedition 403000 missing")
	}
	for range 20 {
		bossDrops := scenarioDrops(template, boss)
		sawShip := false
		for _, drop := range bossDrops {
			if drop.GetType() == dropTypeShip {
				sawShip = true
			}
		}
		if !sawShip {
			t.Fatalf("the boss drops no ship: %v", bossDrops)
		}
	}
}

// TestDrawShip checks a ship draw stays within the chapter's table.
func TestDrawShip(t *testing.T) {
	t.Setenv("BELFAST_DATA", "../../data")
	if err := region.Set("CN"); err != nil {
		t.Fatalf("region: %v", err)
	}
	if err := loadChapterData(); err != nil {
		t.Fatalf("chapter data: %v", err)
	}
	pool := chapterShips(chapterTemplates[403])
	if len(pool) == 0 {
		t.Fatal("4-3 has no ship table")
	}
	known := map[dropRef]bool{}
	for _, ref := range pool {
		known[ref] = true
	}
	for range 100 {
		if ref := drawShip(pool); !known[ref] {
			t.Fatalf("drew %v, not in the table", ref)
		}
	}
}
