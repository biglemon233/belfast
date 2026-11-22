package game

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// weekReset is what a new week changed in the commander's tasks.
type weekReset struct {
	removed []uint32           // bonus tasks the client had (SC_20004)
	added   *protobuf.SC_20003 // the week's bonus tasks, nil when none
}

// ensureDailyTasks gives the commander today's daily missions
// (task_data_template type 3): the ones open at its level, after dropping
// the ones accepted before today. The server counts their progress from the
// task events (battles won, ships built, exercises, backyard food).
func ensureDailyTasks(ctx context.Context, queries *db.Queries, commanderID, level uint32, now time.Time) error {
	start := periodStart(now, 24*time.Hour)
	tasks, err := queries.ListTasks(ctx, commanderID)
	if err != nil {
		return err
	}
	have := map[uint32]bool{}
	var stale []uint32
	for _, task := range tasks {
		if taskConfigs[task.TaskID].Type != taskTypeDaily {
			continue
		}
		if int64(task.AcceptTime) < start.Unix() {
			stale = append(stale, task.TaskID)
		} else {
			have[task.TaskID] = true
		}
	}
	if len(stale) > 0 {
		if err := queries.DeleteTasks(ctx, db.DeleteTasksParams{CommanderID: commanderID, TaskIds: stale}); err != nil {
			return err
		}
	}
	var ids []uint32
	for id, config := range taskConfigs {
		if config.Type == taskTypeDaily && config.Level <= level && !have[id] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	_, err = acceptTasks(ctx, queries, commanderID, ids, uint32(now.Unix()))
	return err
}

// resetWeekIfDue starts the commander's tasks over when a week started
// (Monday 00:00 server time, periodStart) since they were handed out: the
// weekly missions from the first step of each chain open at the
// commander's level, no points and no reward phase claimed, and a new draw
// of TYPE_NEW_WEEKLY bonus tasks (claimed ones included, so any can be
// drawn again). A commander whose week isn't known yet (before migration
// 0061, a new commander) is taken to be in the current one. It returns nil
// when nothing was reset.
func (game *Game) resetWeekIfDue(ctx context.Context, commanderID uint32, now time.Time) (*weekReset, error) {
	week := uint32(periodStart(now, week).Unix())
	var reset *weekReset
	err := game.DB.InTx(ctx, func(queries *db.Queries) error {
		start, err := queries.GetWeeklyWeekStart(ctx, commanderID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if start >= week {
			return nil
		}
		if start == 0 {
			return queries.SetWeeklyWeekStart(ctx, db.SetWeeklyWeekStartParams{CommanderID: commanderID, WeekStart: week})
		}
		commander, err := queries.GetCommander(ctx, commanderID)
		if err != nil {
			return err
		}
		reset = &weekReset{}
		if err := queries.DeleteWeeklyTasks(ctx, commanderID); err != nil {
			return err
		}
		if _, err := unlockWeeklyTasks(ctx, queries, commanderID, commander.Level); err != nil {
			return err
		}
		if err := queries.ClearVirtualItem(ctx, db.ClearVirtualItemParams{CommanderID: commanderID, ItemID: VirtualItemWeeklyPt}); err != nil {
			return err
		}
		if err := queries.ResetWeekly(ctx, db.ResetWeeklyParams{WeekStart: week, CommanderID: commanderID}); err != nil {
			return err
		}

		running, err := queries.ListTasks(ctx, commanderID)
		if err != nil {
			return err
		}
		for _, task := range running {
			if taskConfigs[task.TaskID].Type == taskTypeNewWeekly {
				reset.removed = append(reset.removed, task.TaskID)
			}
		}
		var bonus []uint32
		for id, config := range taskConfigs {
			if config.Type == taskTypeNewWeekly {
				bonus = append(bonus, id)
			}
		}
		slices.Sort(bonus)
		if err := queries.DeleteTasks(ctx, db.DeleteTasksParams{CommanderID: commanderID, TaskIds: bonus}); err != nil {
			return err
		}
		reset.added, err = acceptTasks(ctx, queries, commanderID, weeklyBonusDraw(commander.Level), uint32(now.Unix()))
		return err
	})
	return reset, err
}

// dayWatch pushes the day changes to a client while it plays.
type dayWatch struct {
	mu     sync.Mutex
	timer  *time.Timer
	closed bool
}

// dayWatches are the clients whose day changes are watched, by client.
var dayWatches sync.Map

// watchDays pushes newDay to the client at every midnight, server time,
// until its connection closes. A client already watched (CS_11001 sent
// again on a reconnect) is left as it is.
func (game *Game) watchDays(client *connection.Client) {
	watch := &dayWatch{}
	if _, known := dayWatches.LoadOrStore(client, watch); known {
		return
	}
	game.scheduleDay(client, watch)
	client.OnClose(func() {
		dayWatches.Delete(client)
		watch.mu.Lock()
		defer watch.mu.Unlock()
		watch.closed = true
		watch.timer.Stop()
	})
}

func (game *Game) scheduleDay(client *connection.Client, watch *dayWatch) {
	day := nextDay(time.Now())
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.closed {
		return
	}
	watch.timer = time.AfterFunc(time.Until(day), func() {
		err := client.Run(func(client *connection.Client) error { return game.newDay(client, day) })
		if err == nil {
			game.scheduleDay(client, watch)
		}
	})
}

// newDay is what the server pushes at midnight to a client playing:
//   - on Mondays, the tasks resetWeekIfDue started over: the bonus tasks
//     dropped (SC_20004) and drawn (SC_20003), then the weekly missions
//     whole (SC_20101: TaskProxy inits them again and sends WEEK_TASK_RESET,
//     the one way to also start the reward bar over, which SC_20105's
//     points alone don't);
//   - the tasks a running task activity got since yesterday (SC_20203: the
//     cruise pass opens a battlepass_task_group each week), only for the
//     activities the client already has (TotalTaskProxy reads
//     ActivityProxy:getActivityById(act_id), nil for one that just started);
//   - then SC_20015: the server's day changed, ProxyRegister runs its
//     DayCall right away (TaskProxy accepts the activity tasks again,
//     TotalTaskProxy drops the ended ones) instead of after 30 seconds.
//
// Never captured: built from the Lua alone.
func (game *Game) newDay(client *connection.Client, day time.Time) error {
	ctx := context.Background()
	reset, err := game.resetWeekIfDue(ctx, client.CommanderID, day)
	if err != nil {
		return err
	}
	if reset != nil {
		if len(reset.removed) > 0 {
			if err := client.Push(&protobuf.SC_20004{IdList: reset.removed}); err != nil {
				return err
			}
		}
		if reset.added != nil {
			if err := client.Push(reset.added); err != nil {
				return err
			}
		}
		weekly, err := game.weeklyInfo(ctx, game.DB.Queries, client.CommanderID)
		if err != nil {
			return err
		}
		if err := client.Push(&protobuf.SC_20101{Info: weekly}); err != nil {
			return err
		}
	}
	if added := newActivityTasks(day); len(added.Info) > 0 {
		if err := client.Push(added); err != nil {
			return err
		}
	}
	return client.Push(&protobuf.SC_20015{Time: proto.Uint32(uint32(day.Unix()))})
}

// newActivityTasks are the tasks the task activities running both before
// day and after it got at day, with no progress yet. A cruise week opens a
// second past midnight (GetServerOverWeek is a ceiling).
func newActivityTasks(day time.Time) *protobuf.SC_20203 {
	before := map[uint32][]uint32{}
	for _, activity := range openTaskActivities(day.Add(-time.Second)) {
		for _, task := range activity.tasks {
			before[activity.id] = append(before[activity.id], task.taskID)
		}
	}
	added := &protobuf.SC_20203{}
	for _, activity := range openTaskActivities(day.Add(time.Second)) {
		had, ok := before[activity.id]
		if !ok {
			continue
		}
		info := &protobuf.ACT_TASK_LIST{ActId: proto.Uint32(activity.id)}
		for _, task := range activity.tasks {
			if !slices.Contains(had, task.taskID) {
				info.Tasks = append(info.Tasks, &protobuf.ACT_TASK{Id: proto.Uint32(task.taskID), Progress: proto.Uint32(0)})
			}
		}
		if len(info.Tasks) > 0 {
			added.Info = append(added.Info, info)
		}
	}
	return added
}
