package game

import (
	"slices"
	"testing"
)

// TestRewardPageData checks the two new commander reward pages' tables load
// and name what the pages read: the level 50 row of activity_level_award 1,
// and 5-4 (chapter 504) in activity_event_chapter_award 368's list.
func TestRewardPageData(t *testing.T) {
	t.Setenv("BELFAST_DATA", "../../data")
	if err := loadTaskData(); err != nil {
		t.Fatalf("load the game data: %v", err)
	}
	if err := loadRewardPageData(); err != nil {
		t.Fatalf("load the reward page data: %v", err)
	}
	row, ok := levelAwards[1].row(50)
	if !ok || len(row.Drops) == 0 {
		t.Fatalf("activity_level_award 1 has no level 50 row: %+v", levelAwards[1].rows)
	}
	page, ok := chapterAwards[368]
	if !ok {
		t.Fatal("activity_event_chapter_award 368 missing")
	}
	index := slices.Index(page.Chapter, 504)
	if index < 0 || index >= len(page.AwardDisplay) || len(page.AwardDisplay[index]) == 0 {
		t.Fatalf("chapter 504 not payable: chapter=%v awards=%d", page.Chapter, len(page.AwardDisplay))
	}
}
