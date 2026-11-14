package game

import "testing"

// TestKillPageActivity checks which type-74 activities count a battle's kills
// in their data1: only the kill pages, whose activity_event_pt row has no PT
// item and opens its phases by day. A generic PT buff activity (50261,
// HelenaPTPage) must not be one, or kills would pay its PT rewards.
func TestKillPageActivity(t *testing.T) {
	t.Setenv("BELFAST_DATA", "../../data")
	if err := loadTaskData(); err != nil {
		t.Fatalf("load the game data: %v", err)
	}
	kill, ok := activityConfigs[51303]
	if !ok {
		t.Fatal("activity 51303 is not in the data")
	}
	if !killPageActivity(kill) {
		t.Error("51303 (TwoPointFiveKillPage) should count kills")
	}
	if pt, ok := activityConfigs[50261]; ok && killPageActivity(pt) {
		t.Error("50261 (HelenaPTPage) is a PT activity, it must not count kills")
	}
}
