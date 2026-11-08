package game

import (
	"testing"

	"github.com/ggmolly/belfast/internal/region"
)

// TestChapterBattleDrops checks a won battle's loot comes from the enemy's own
// award_display (4-3): a normal enemy drops a design or a gear part besides
// the coins, the boss adds the chapter's ship.
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
	drops := scenarioDrops(template, normal)
	if len(drops) != 2 {
		t.Fatalf("a normal battle gives coins and one item, got %d drops", len(drops))
	}
	if drops[0].GetType() != dropTypeResource || drops[1].GetType() == dropTypeResource {
		t.Fatalf("unexpected drops %v", drops)
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

	boss, ok := expeditionTemplates[403000]
	if !ok {
		t.Fatal("expedition 403000 missing")
	}
	bossDrops := scenarioDrops(template, boss)
	if len(bossDrops) < 4 {
		t.Fatalf("a boss battle gives coins, the core, items and a ship, got %d", len(bossDrops))
	}
	sawShip := false
	for _, drop := range bossDrops {
		if drop.GetType() == dropTypeShip {
			sawShip = true
		}
	}
	if !sawShip {
		t.Fatal("the boss drops no ship")
	}
}
