package game

import (
	"testing"
	"time"

	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/region"
)

// TestSplitDailyTasks checks a day drops yesterday's daily missions whether
// they were claimed or not: a claimed one keeps its submit_time, so a reset
// that only looked at the unclaimed rows would never offer it again.
func TestSplitDailyTasks(t *testing.T) {
	t.Setenv("BELFAST_DATA", "../../data")
	if err := region.Set("CN"); err != nil {
		t.Fatalf("region: %v", err)
	}
	if err := loadTaskData(); err != nil {
		t.Fatalf("task data: %v", err)
	}
	day := time.Date(2026, 11, 25, 0, 0, 0, 0, time.Local)
	start := periodStart(day, 24*time.Hour)
	// 7201 and 7202 are daily missions, 23001 a commander manual task
	if taskConfigs[7201].Type != taskTypeDaily || taskConfigs[7202].Type != taskTypeDaily {
		t.Fatal("7201/7202 are not daily missions")
	}
	tasks := []db.ListTasksRow{
		{TaskID: 7201, AcceptTime: uint32(day.Unix())},
		{TaskID: 23001, AcceptTime: uint32(day.Add(-48 * time.Hour).Unix())},
	}
	claimed := []db.ListSubmittedTasksRow{
		{TaskID: 7202, AcceptTime: uint32(day.Add(-48 * time.Hour).Unix()), SubmitTime: uint32(day.Add(-47 * time.Hour).Unix())},
	}
	stale, have := splitDailyTasks(tasks, claimed, start.Unix())
	if len(stale) != 1 || stale[0] != 7202 {
		t.Fatalf("stale = %v, want the claimed daily mission 7202", stale)
	}
	if !have[7201] {
		t.Fatalf("have = %v, want today's daily mission 7201", have)
	}
	if have[7202] {
		t.Fatalf("a dropped daily mission must not count as one the commander has")
	}
	if have[23001] {
		t.Fatalf("a task that isn't a daily mission must be left alone")
	}
}
