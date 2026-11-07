package game

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"math/rand/v2"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// task_data_template / weekly_task_template sub_type: what a task counts.
// Requests record them on their Tx when they happen.
const (
	TaskSubDefeatEnemy   = 11   // enemy ships sunk in a battle
	TaskSubDailyStage    = 26   // daily challenge battles won, target: stage id
	TaskSubExercise      = 27   // exercises fought, target: exerciseTaskWin for a win
	TaskSubWin           = 20   // battles won
	TaskSubDefeatBoss    = 21   // boss fleets defeated
	TaskSubWinA          = 23   // battles won with A rank or better
	TaskSubWinS          = 24   // battles won with S rank
	TaskSubBuild         = 30   // ships built
	TaskSubRetireShip    = 31   // ships retired
	TaskSubEnhanceGear   = 40   // gear upgrades
	TaskSubScrapGear     = 41   // pieces of gear disassembled
	TaskSubCraftGear     = 42   // gear crafted
	TaskSubAcademyLevel  = 85   // canteen / merchant level (absolute)
	TaskSubResearch      = 110  // Research Academy projects claimed
	TaskSubHandbookPhase = 114  // commander manual tasks claimed, target: page id
	TaskSubUseItem       = 120  // items used, target: item id
	TaskSubSpendOil      = 121  // oil spent
	TaskSubObtainItem    = 130  // items obtained, target: item id
	TaskSubWeeklyPt      = 161  // weekly mission points obtained
	TaskSubLevel         = 1011 // commander level (absolute)
	TaskSubClearStage    = 1020 // stage cleared, target: chapter id
	TaskSubCruisePt      = 1023 // cruise pass points (absolute)
	TaskSubStars         = 1026 // main campaign stars (absolute)
	TaskSubSortieExp     = 1041 // ship EXP earned from sorties
	TaskSubTechPoints    = 1050 // Fleet Technology points (absolute), target: nation id, 0 for all
	// the commander manual's "what the commander already has" tasks: their
	// progress is a value read off the save, not an event (see syncStateTasks)
	TaskSubShipLevel    = 1013 // a ship at level target_num or above
	TaskSubShipLevel2   = 1017 // a ship at level target_id_2 or above
	TaskSubBreakoutStar = 1027 // the furthest breakout a ship has reached
	// a permanent activity started (活动档案), the only task counting it
	TaskSubPermanentActivity = 420
	// tech packs used (item_data_statistics type 5)
	TaskSubTechBox = 50
	// ship breakouts done (UpgradeStarCommand)
	TaskSubBreakout = 32
	// a gift package shop purchase (shop_template genre gift_package)
	TaskSubGiftShop = 154
	// the manual's shop tasks; the shop each one means was read off its desc
	// and the shop screens (军火 → the medal exchange, 舰队 → the guild shop,
	// 核心 → the monthly core shop, 军需 → requisition)
	TaskSubArmoryShop = 150
	TaskSubFleetShop  = 151
	TaskSubCoreShop   = 152
	TaskSubNavyShop   = 155
	// a daily challenge cleared (FinishDailyCommand)
	TaskSubDailyClear = 186
	// a gear skin worn (ShipEquipSkinCommand)
	TaskSubGearSkin = 47
	// a hard mode chapter cleared (困难海域), the daily 7205's event
	TaskSubHardClear = 185
)

// Sub types whose event carries the current value (a level, a total) rather
// than an amount to add. Progress is counted by the server: neither the Lua
// (Task.getProgress, model/vo/task/task.lua, only computes the sub types the
// client can count itself) nor task_data_template says it; their descs do
// ("Reach Commander lv. 15", "Upgrade the Canteen or the Merchant to Level
// 2", "Gain a total of 12 stars").
var taskAbsoluteSubTypes = map[uint32]bool{
	TaskSubAcademyLevel: true,
	TaskSubLevel:        true,
	TaskSubCruisePt:     true,
	TaskSubStars:        true,
	TaskSubTechPoints:   true,
	TaskSubShipLevel:    true,
	TaskSubShipLevel2:   true,
	TaskSubBreakoutStar: true,
	TaskSubCityDPS:      true,
	TaskSubCityWave:     true,
	// the auction's: its best lot and its coins
	TaskSubAuctionLot:      true,
	TaskSubAuctionLotTotal: true,
	TaskSubAuctionGold:     true,
}

// Sub types a newly accepted task starts from what the commander already did.
// In the capture, tasks accepted mid-tutorial came with the commander level,
// stages cleared, stars and academy level, and with the build count unless
// they are auto_commit (the manual's "Build 1 ship" started at 0 when "Build
// 10 times in total" started at 1), while gear crafts / upgrades, wins and
// kills started back at 0. Server behaviour no table holds:
// task_data_template.count_inherit names one earlier task to take the count
// of, not a sub type.
var taskInheritedSubTypes = map[uint32]bool{
	TaskSubAcademyLevel: true,
	TaskSubLevel:        true,
	TaskSubClearStage:   true,
	TaskSubCruisePt:     true,
	TaskSubStars:        true,
	TaskSubTechPoints:   true,
	TaskSubCityDPS:      true,
	TaskSubCityWave:     true,
	TaskSubAuctionGold:  true,
}

// item_virtual_data_statistics ids earned from tasks (drop type 8)
const (
	VirtualItemWeeklyPt = 59012 // weekly mission points (WeekTaskProgress)
	VirtualItemCruisePt = 59599 // cruise pass points (SubmitActiveTaskCommand)
)

// ActivityConst.ACTIVITY_TYPE_* of the activities whose tasks come in
// SC_20201 (TotalTaskProxy)
const (
	activityTypeTasks             = 13
	activityTypeCruise            = 54 // PT_CRUSING
	activityTypePtHei5            = 130
	activityTypeBossRushDalCollab = 132
	activityTypeTaskRyza          = 89
	activityTypeHotspring2        = 100
	activityTypeNotTraceable      = 121
)

// taskActivityTypes are the activity_template types whose tasks come in
// SC_20201: TaskProxy holds those of the first four (TotalTaskProxy's
// normal_task_type, model/proxy/totaltaskproxy.lua), ActivityTaskProxy the
// others' (activity_task_type).
var taskActivityTypes = map[uint32]bool{
	activityTypeTasks: true, activityTypeCruise: true, activityTypePtHei5: true, activityTypeBossRushDalCollab: true,
	activityTypeTaskRyza: true, activityTypeHotspring2: true, activityTypeNotTraceable: true,
}

// normalTaskActivityTypes are the taskActivityTypes whose tasks TaskProxy
// holds (normal_task_type): what CS_20207 and CS_20209 are about.
var normalTaskActivityTypes = map[uint32]bool{
	activityTypeTasks: true, activityTypeCruise: true, activityTypePtHei5: true, activityTypeBossRushDalCollab: true,
}

// TaskEvent is something that happened that tasks count.
type TaskEvent struct {
	SubType uint32 // task_data_template.sub_type, see TaskSub*
	Target  uint32 // chapter id, item id...; 0 when the event has none
	Count   uint32 // amount to add, or the new value for absolute sub types
}

// taskList is SC_20001, TaskProxy's tasks: the running ones, then the
// research ships' claimed ones with their submit_time, which TaskProxy
// keeps as finished (initTaskInfo's finishData): the Dev. Dock reads a
// research's state from them (ShipBluePrint:getTaskStateById,
// isFinishPrevTask). So are the claimed tasks of a running mini-event's task
// list (Activity:canPermanentFinish) and of a running returning player
// campaign (RefluxTaskView).
func taskList(ctx context.Context, queries *db.Queries, commanderID uint32) (*protobuf.SC_20001, error) {
	tasks, err := queries.ListTasks(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	kept, err := permanentClaimedTasks(ctx, queries, commanderID)
	if err != nil {
		return nil, err
	}
	reflux, err := refluxClaimedTasks(ctx, queries, commanderID, time.Now())
	if err != nil {
		return nil, err
	}
	maps.Copy(kept, reflux)
	msg := &protobuf.SC_20001{Info: make([]*protobuf.TASKINFO, len(tasks))}
	for i, task := range tasks {
		msg.Info[i] = &protobuf.TASKINFO{
			Id:         proto.Uint32(task.TaskID),
			Progress:   proto.Uint32(task.Progress),
			AcceptTime: proto.Uint32(task.AcceptTime),
		}
	}
	claimed, err := queries.ListSubmittedTasks(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	for _, task := range claimed {
		if blueprintTask(task.TaskID) || kept[task.TaskID] {
			msg.Info = append(msg.Info, &protobuf.TASKINFO{
				Id:         proto.Uint32(task.TaskID),
				Progress:   proto.Uint32(task.Progress),
				AcceptTime: proto.Uint32(task.AcceptTime),
				SubmitTime: proto.Uint32(task.SubmitTime),
			})
		}
	}
	return msg, nil
}

// activityTasksList is SC_20201, the tasks of the open task activities
// (TotalTaskProxy).
func (game *Game) activityTasksList(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (*protobuf.SC_20201, error) {
	activities, err := game.activityTaskLists(ctx, queries, commanderID, now)
	if err != nil {
		return nil, err
	}
	msg := &protobuf.SC_20201{}
	for _, activity := range activities {
		info := &protobuf.ACT_TASK_INIT_LIST{ActId: proto.Uint32(activity.id)}
		for _, task := range activity.tasks {
			if task.finished {
				info.FinishIds = append(info.FinishIds, task.taskID)
				continue
			}
			info.Tasks = append(info.Tasks, &protobuf.ACT_TASK{Id: proto.Uint32(task.taskID), Progress: proto.Uint32(task.progress)})
		}
		msg.Info = append(msg.Info, info)
	}
	return msg, nil
}

// weeklyInfo is SC_20101.info, the weekly missions: their progress, the
// points earned and the reward level claimed.
func (game *Game) weeklyInfo(ctx context.Context, queries *db.Queries, commanderID uint32) (*protobuf.WEEKLY_INFO, error) {
	tasks, err := queries.ListWeeklyTasks(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	rewardLevel, err := queries.GetWeeklyRewardLevel(ctx, commanderID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	pt, err := queries.GetVirtualItem(ctx, db.GetVirtualItemParams{CommanderID: commanderID, ItemID: VirtualItemWeeklyPt})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	info := &protobuf.WEEKLY_INFO{Pt: proto.Uint32(pt), RewardLv: proto.Uint32(rewardLevel)}
	for _, task := range tasks {
		info.Task = append(info.Task, &protobuf.WEEKLY_TASK_P20{Id: proto.Uint32(task.TaskID), Progress: proto.Uint32(task.Progress)})
	}
	return info, nil
}

// Task types (task_data_template type, model/vo/task/task.lua)
const (
	taskTypeScenario  = 1  // Task.TYPE_SCENARIO, the main story
	taskTypeNewWeekly = 13 // Task.TYPE_NEW_WEEKLY
)

// starterTasks are the tasks besides the commander manual unlock ones a new
// commander at level gets: the head of the main story chain (TYPE_SCENARIO
// with is_head, task 1 "An Exercise") and a draw of weeklyBonusTasks
// TYPE_NEW_WEEKLY tasks open at level (the capture drew 7307 and 7310 out of
// 7304, 7306, 7307, 7310; weekly_fixed_task 7311 is level 80).
func starterTasks(level uint32) []uint32 {
	var tasks []uint32
	for id, config := range taskConfigs {
		if config.Level <= level && config.Type == taskTypeScenario && config.IsHead == 1 {
			tasks = append(tasks, id)
		}
	}
	slices.Sort(tasks)
	return append(tasks, weeklyBonusDraw(level)...)
}

// weeklyFixedTasksAt are the weeklyFixedTasks open at level.
func weeklyFixedTasksAt(level uint32) []uint32 {
	var ids []uint32
	for _, id := range weeklyFixedTasks {
		if config, ok := taskConfigs[id]; ok && config.Level <= level {
			ids = append(ids, id)
		}
	}
	return ids
}

// weeklyBonusDraw draws the weeklyBonusTasks TYPE_NEW_WEEKLY tasks a
// commander at level is offered for a week, after the weeklyFixedTasks
// open at level.
func weeklyBonusDraw(level uint32) []uint32 {
	var fixed, bonus []uint32
	for id, config := range taskConfigs {
		switch {
		case config.Level > level || config.Type != taskTypeNewWeekly:
		case slices.Contains(weeklyFixedTasks, id):
			fixed = append(fixed, id)
		default:
			bonus = append(bonus, id)
		}
	}
	slices.Sort(fixed)
	slices.Sort(bonus)
	rand.Shuffle(len(bonus), func(i, j int) { bonus[i], bonus[j] = bonus[j], bonus[i] })
	return append(fixed, bonus[:min(int(weeklyBonusTasks), len(bonus))]...)
}

// grantTasksStarter gives a new commander the capture's SC_20001 tasks: the
// tasks that unlock commander manual pages (tutorial_handbook_task.unlock,
// 23501-23517) and starterTasks, plus the first step of every weekly mission
// chain open at level 1 (weekly_task_template, 10001, 10011, ... 10141).
func grantTasksStarter(tx *Tx, commander db.Commander) error {
	ctx, queries := tx.ctx, tx.Queries
	// what a new commander already is, for tasks that start from it
	for _, counter := range []TaskEvent{{SubType: TaskSubLevel, Count: commander.Level}, {SubType: TaskSubAcademyLevel, Count: 1}} {
		if err := queries.SetTaskCounter(ctx, db.SetTaskCounterParams{CommanderID: commander.ID, SubType: counter.SubType, Value: counter.Count}); err != nil {
			return err
		}
	}
	tasks := append(slices.Clone(handbookUnlockTasks), starterTasks(commander.Level)...)
	if _, err := acceptTasks(ctx, queries, commander.ID, tasks, uint32(commander.RegisteredAt)); err != nil {
		return err
	}
	if _, err := unlockWeeklyTasks(ctx, queries, commander.ID, commander.Level); err != nil {
		return err
	}
	return queries.SetWeeklyRewardLevel(ctx, db.SetWeeklyRewardLevelParams{CommanderID: commander.ID, RewardLv: 0})
}

// unlockWeeklyTasks adds the first step of the weekly mission chains (ids
// grouped by tens: 10001-10007, 10011-10016...) open at level that the
// commander doesn't have yet, and returns them.
func unlockWeeklyTasks(ctx context.Context, queries *db.Queries, commanderID uint32, level uint32) ([]uint32, error) {
	// claimed steps included: a chain claimed to its end isn't given again
	current, err := queries.ListWeeklyTaskIDs(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	have := map[uint32]bool{}
	for _, id := range current {
		have[id/10] = true
	}
	first := map[uint32]weeklyTaskConfig{}
	for _, config := range weeklyTaskConfigs {
		if step, ok := first[config.ID/10]; !ok || config.ID < step.ID {
			first[config.ID/10] = config
		}
	}
	var added []uint32
	for chain, step := range first {
		if have[chain] || step.Level > level {
			continue
		}
		if err := queries.CreateWeeklyTask(ctx, db.CreateWeeklyTaskParams{CommanderID: commanderID, TaskID: step.ID, Progress: 0}); err != nil {
			return nil, err
		}
		added = append(added, step.ID)
	}
	slices.Sort(added)
	return added, nil
}

type taskCounterKey struct{ subType, target uint32 }

func listTaskCounters(ctx context.Context, queries *db.Queries, commanderID uint32) (map[taskCounterKey]uint32, error) {
	rows, err := queries.ListTaskCounters(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	counters := make(map[taskCounterKey]uint32, len(rows))
	for _, row := range rows {
		counters[taskCounterKey{row.SubType, row.Target}] = row.Value
	}
	return counters, nil
}

// taskCounter is one of the commander's task counters, 0 when nothing
// counted for it yet.
func taskCounter(ctx context.Context, queries *db.Queries, commanderID, subType, target uint32) (uint32, error) {
	value, err := queries.GetTaskCounter(ctx, db.GetTaskCounterParams{CommanderID: commanderID, SubType: subType, Target: target})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return value, err
}

// cruisePt is the commander's cruise pass points (virtual item 59599),
// stored as their TaskSubCruisePt counter: the stored value, or the last
// total tx recorded when it gave some already.
func (tx *Tx) cruisePt() (uint32, error) {
	for _, event := range slices.Backward(tx.events) {
		if event.SubType == TaskSubCruisePt {
			return event.Count, nil
		}
	}
	return taskCounter(tx.ctx, tx.Queries, tx.commanderID, TaskSubCruisePt, 0)
}

// initialProgress is where a task accepted now starts, see
// taskInheritedSubTypes.
func initialProgress(config taskConfig, counters map[taskCounterKey]uint32) uint32 {
	if !taskInheritedSubTypes[config.SubType] && !(config.SubType == TaskSubBuild && config.AutoCommit == 0) {
		return 0
	}
	target := config.target
	var progress uint32
	switch {
	case target.any || target.min != nil:
		progress = counters[taskCounterKey{config.SubType, 0}]
	default:
		for _, id := range target.ids {
			value := counters[taskCounterKey{config.SubType, id}]
			if taskAbsoluteSubTypes[config.SubType] {
				progress = max(progress, value)
			} else {
				progress += value
			}
		}
	}
	return min(progress, config.TargetNum)
}

// acceptTasks gives the commander the tasks they don't have yet and returns
// them as SC_20003 (nil when there is none). Unknown task ids are skipped.
func acceptTasks(ctx context.Context, queries *db.Queries, commanderID uint32, ids []uint32, now uint32) (*protobuf.SC_20003, error) {
	counters, err := listTaskCounters(ctx, queries, commanderID)
	if err != nil {
		return nil, err
	}
	added := &protobuf.SC_20003{}
	for _, id := range ids {
		config, ok := taskConfigs[id]
		if !ok {
			continue
		}
		progress := initialProgress(config, counters)
		created, err := queries.CreateTask(ctx, db.CreateTaskParams{CommanderID: commanderID, TaskID: id, Progress: progress, AcceptTime: now})
		if err != nil {
			return nil, err
		}
		if created == 0 {
			continue
		}
		added.Info = append(added.Info, &protobuf.TASK_ADD{
			Id:         proto.Uint32(id),
			Progress:   proto.Uint32(progress),
			AcceptTime: proto.Uint32(now),
			SubmitTime: proto.Uint32(0),
		})
	}
	if len(added.Info) == 0 {
		return nil, nil
	}
	return added, nil
}

// acceptTasksPush gives the commander tasks (e.g. the ones an activity hands
// out) and pushes them (SC_20003).
func (game *Game) acceptTasksPush(ctx context.Context, client *connection.Client, ids ...uint32) error {
	added, err := acceptTasks(ctx, game.DB.Queries, client.CommanderID, ids, uint32(time.Now().Unix()))
	if err != nil || added == nil {
		return err
	}
	return client.Push(added)
}

// applyTaskEvents stores events (counters and task progress) and returns the
// progress pushes to send: SC_20002 (tasks), SC_20102 (weekly missions),
// SC_20103 (weekly missions unlocked by a level up), SC_20202 (activity
// tasks). Game.tx runs it for the events a request records.
func (game *Game) applyTaskEvents(ctx context.Context, queries *db.Queries, commanderID uint32, events []TaskEvent) ([]proto.Message, error) {
	if len(events) == 0 {
		return nil, nil
	}
	counters, err := listTaskCounters(ctx, queries, commanderID)
	if err != nil {
		return nil, err
	}
	changed := map[taskCounterKey]bool{}
	subTypes := map[uint32]bool{}
	var level uint32
	for _, event := range events {
		subTypes[event.SubType] = true
		keys := []taskCounterKey{{event.SubType, event.Target}}
		if event.Target != 0 {
			keys = append(keys, taskCounterKey{event.SubType, 0})
		}
		for _, key := range keys {
			switch {
			case !taskAbsoluteSubTypes[event.SubType]:
				counters[key] += event.Count
			case key.target == event.Target || event.Count > counters[key]:
				counters[key] = event.Count
			}
			changed[key] = true
		}
		if event.SubType == TaskSubLevel {
			level = event.Count
		}
	}
	for key := range changed {
		if err := queries.SetTaskCounter(ctx, db.SetTaskCounterParams{CommanderID: commanderID, SubType: key.subType, Target: key.target, Value: counters[key]}); err != nil {
			return nil, err
		}
	}

	// progress returns the task's progress after the events, and whether any
	// of them counted for it
	progress := func(subType uint32, target taskTarget, current uint32) (uint32, bool) {
		counted := false
		for _, event := range events {
			if event.SubType != subType || !target.matches(event.Target) {
				continue
			}
			counted = true
			if taskAbsoluteSubTypes[subType] {
				current = event.Count
			} else {
				current += event.Count
			}
		}
		return current, counted
	}

	var pushes []proto.Message
	// the commander's tasks are only read when a task counts an event's
	// sub type at all
	counts := func(idsBySubType map[uint32][]uint32) bool {
		for subType := range subTypes {
			if len(idsBySubType[subType]) > 0 {
				return true
			}
		}
		return false
	}
	if counts(taskIDsBySubType) {
		tasks, err := queries.ListTasks(ctx, commanderID)
		if err != nil {
			return nil, err
		}
		taskProgress := &protobuf.SC_20002{}
		for _, task := range tasks {
			config, ok := taskConfigs[task.TaskID]
			if !ok {
				continue
			}
			value, counted := progress(config.SubType, config.target, task.Progress)
			if !counted {
				continue
			}
			// TaskProxy tasks stop at their target (the second win re-sends
			// "An Exercise" at 1/1)
			value = min(value, config.TargetNum)
			if err := queries.SetTaskProgress(ctx, db.SetTaskProgressParams{Progress: value, CommanderID: commanderID, TaskID: task.TaskID}); err != nil {
				return nil, err
			}
			taskProgress.Info = append(taskProgress.Info, &protobuf.TASK_PROGRESS{Id: proto.Uint32(task.TaskID), Progress: proto.Uint32(value)})
		}
		if len(taskProgress.Info) > 0 {
			pushes = append(pushes, taskProgress)
		}
	}

	if counts(weeklyTaskIDsBySubType) {
		weekly, err := queries.ListWeeklyTasks(ctx, commanderID)
		if err != nil {
			return nil, err
		}
		weeklyProgress := &protobuf.SC_20102{}
		for _, task := range weekly {
			config, ok := weeklyTaskConfigs[task.TaskID]
			if !ok {
				continue
			}
			// weekly missions keep counting past their target (2/1 wins in the
			// capture)
			value, counted := progress(config.SubType, config.target, task.Progress)
			if !counted {
				continue
			}
			if err := queries.SetWeeklyTaskProgress(ctx, db.SetWeeklyTaskProgressParams{Progress: value, CommanderID: commanderID, TaskID: task.TaskID}); err != nil {
				return nil, err
			}
			weeklyProgress.Task = append(weeklyProgress.Task, &protobuf.WEEKLY_TASK_P20{Id: proto.Uint32(task.TaskID), Progress: proto.Uint32(value)})
		}
		if len(weeklyProgress.Task) > 0 {
			pushes = append(pushes, weeklyProgress)
		}
	}
	if level > 0 {
		unlocked, err := unlockWeeklyTasks(ctx, queries, commanderID, level)
		if err != nil {
			return nil, err
		}
		if len(unlocked) > 0 {
			pushes = append(pushes, &protobuf.SC_20103{Id: unlocked})
		}
		// the research ships' opening tasks of the level
		opened, err := acceptTasks(ctx, queries, commanderID, blueprintOpenTasksAt(level), uint32(time.Now().Unix()))
		if err != nil {
			return nil, err
		}
		if opened != nil {
			pushes = append(pushes, opened)
		}
	}

	// the same for the open activities' tasks
	activities := openTaskActivities(time.Now())
	activityCounts := false
	for _, activity := range activities {
		for _, task := range activity.tasks {
			config, ok := taskConfigs[task.taskID]
			activityCounts = activityCounts || (ok && subTypes[config.SubType])
		}
	}
	if !activityCounts {
		return pushes, nil
	}
	if err := activityTaskProgress(ctx, queries, commanderID, activities); err != nil {
		return nil, err
	}
	activityProgress := &protobuf.SC_20202{}
	for _, activity := range activities {
		info := &protobuf.ACT_TASK_LIST{ActId: proto.Uint32(activity.id)}
		for _, task := range activity.tasks {
			config, ok := taskConfigs[task.taskID]
			if !ok || task.finished {
				continue
			}
			value, counted := progress(config.SubType, config.target, task.progress)
			if !counted {
				continue
			}
			if err := queries.SetActivityTaskProgress(ctx, db.SetActivityTaskProgressParams{CommanderID: commanderID, ActID: activity.id, TaskID: task.taskID, Progress: value}); err != nil {
				return nil, err
			}
			info.Tasks = append(info.Tasks, &protobuf.ACT_TASK{Id: proto.Uint32(task.taskID), Progress: proto.Uint32(value)})
		}
		if len(info.Tasks) > 0 {
			activityProgress.Info = append(activityProgress.Info, info)
		}
	}
	if len(activityProgress.Info) > 0 {
		pushes = append(pushes, activityProgress)
	}
	return pushes, nil
}

type activityTask struct {
	taskID   uint32
	progress uint32
	finished bool
}

type taskActivity struct {
	id    uint32
	tasks []activityTask
}

// activityTaskLists are the open task activities with their tasks and the
// commander's progress on them.
func (game *Game) activityTaskLists(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) ([]taskActivity, error) {
	activities := openTaskActivities(now)
	if err := activityTaskProgress(ctx, queries, commanderID, activities); err != nil {
		return nil, err
	}
	return activities, nil
}

// openTaskActivities are the task activities (activity_template rows of
// taskActivityTypes) running at now with their tasks, without progress.
func openTaskActivities(now time.Time) []taskActivity {
	var activities []taskActivity
	for _, config := range taskActivities {
		if !config.Time.Open(now) {
			continue
		}
		activity := taskActivity{id: config.ID}
		for _, taskID := range activityTaskIDs(config, now) {
			activity.tasks = append(activity.tasks, activityTask{taskID: taskID})
		}
		activities = append(activities, activity)
	}
	return activities
}

// activityTaskProgress fills in the commander's progress on activities' tasks.
func activityTaskProgress(ctx context.Context, queries *db.Queries, commanderID uint32, activities []taskActivity) error {
	rows, err := queries.ListActivityTasks(ctx, commanderID)
	if err != nil {
		return err
	}
	stored := map[[2]uint32]db.ListActivityTasksRow{}
	for _, row := range rows {
		stored[[2]uint32{row.ActID, row.TaskID}] = row
	}
	for _, activity := range activities {
		for i, task := range activity.tasks {
			row := stored[[2]uint32{activity.id, task.taskID}]
			activity.tasks[i].progress, activity.tasks[i].finished = row.Progress, row.Finished
		}
	}
	return nil
}

// activityTaskIDs lists an activity's tasks: config_data holds them, except
// for the cruise pass where it holds battlepass_task_group ids, a group
// opening each week (group_mask) since the activity started
// (CrusingTaskLayer, GetServerOverWeek).
func activityTaskIDs(config activityConfig, now time.Time) []uint32 {
	if config.Type != activityTypeCruise {
		return config.tasks
	}
	elapsed := now.Sub(periodStart(config.Time.Start, week)).Truncate(time.Second)
	weeks := uint32((elapsed + week - 1) / week)
	var tasks []uint32
	for _, groupID := range config.tasks {
		group, ok := battlepassTaskGroups[groupID]
		if !ok || (group.IsOntime == 1 && group.GroupMask > weeks) {
			continue
		}
		tasks = append(tasks, group.tasks...)
	}
	return tasks
}

// awardChoice picks the award_choice option the client chose (CS_20005
// choice_award, for tasks with selectable rewards), nil if it isn't one.
func awardChoice(config taskConfig, chosen []*protobuf.DROPINFO) [][]uint32 {
	var options [][][]uint32
	if len(chosen) == 0 || json.Unmarshal(config.AwardChoice, &options) != nil {
		return nil
	}
	for _, option := range options {
		if len(option) != len(chosen) {
			continue
		}
		same := true
		for i, drop := range option {
			if len(drop) < 3 || drop[0] != chosen[i].GetType() || drop[1] != chosen[i].GetId() || drop[2] != chosen[i].GetNumber() {
				same = false
				break
			}
		}
		if same {
			return option
		}
	}
	return nil
}

// handleSubmitTask claims a finished task's reward (SubmitTaskCommand). The
// progress the rewards cause and the tasks it leads to (next_task) are pushed
// before the reply, as in the capture; the client drops the task itself.
func (game *Game) handleSubmitTask(client *connection.Client, req *protobuf.CS_20005) error {
	ctx := context.Background()
	claim, ok, err := game.taskClaim(ctx, client.CommanderID, req.GetId())
	if err != nil {
		return err
	}
	if ok && len(req.GetChoiceAward()) > 0 {
		claim.drops = awardChoice(claim.config, req.GetChoiceAward())
		ok = claim.drops != nil
	}
	if !ok {
		logger.WithFields("Task", logger.FieldValue("id", req.GetId()), logger.FieldValue("progress", claim.task.Progress), logger.FieldValue("target", claim.config.TargetNum)).Warn("claim refused")
		return client.Send(&protobuf.SC_20006{Result: result(errRefused)})
	}
	awards, err := game.claimTasks(ctx, client, []taskClaimed{claim}, nil)
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_20006{Result: result(err), AwardList: awards})
}

// handleSubmitTasks claims several finished tasks at once
// (SubmitTaskOneStepCommand, the missions' claim all): the claimed ids and
// their rewards together. The client checked them all, a task that can't be
// claimed is left out.
func (game *Game) handleSubmitTasks(client *connection.Client, req *protobuf.CS_20011) error {
	ctx := context.Background()
	reply := &protobuf.SC_20012{}
	var claims []taskClaimed
	for _, id := range req.GetIdList() {
		claim, ok, err := game.taskClaim(ctx, client.CommanderID, id)
		if err != nil {
			return err
		}
		if ok && !slices.Contains(reply.IdList, id) {
			claims = append(claims, claim)
			reply.IdList = append(reply.IdList, id)
		}
	}
	awards, err := game.claimTasks(ctx, client, claims, nil)
	if refused(err) {
		// a "Gather" task the commander can't pay: nothing claimed
		return client.Send(&protobuf.SC_20012{})
	} else if err != nil {
		return err
	}
	reply.AwardList = awards
	return client.Send(reply)
}

// taskClaimed is a finished, unclaimed task and the drops claiming it gives.
type taskClaimed struct {
	task   db.CommanderTask
	config taskConfig
	drops  [][]uint32
}

// taskClaim is task id if the commander can claim it.
func (game *Game) taskClaim(ctx context.Context, commanderID, id uint32) (taskClaimed, bool, error) {
	config, ok := taskConfigs[id]
	task, err := game.DB.GetTask(ctx, db.GetTaskParams{CommanderID: commanderID, TaskID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return taskClaimed{}, false, nil
	}
	if err != nil {
		return taskClaimed{}, false, err
	}
	// a "Gather" task's progress is what the commander owns, paid on claim
	reached := task.Progress >= config.TargetNum || taskGiveCost(config) != nil
	ok = ok && task.SubmitTime == 0 && reached
	drops := config.awards
	refluxDrops, isReflux, unlocked, err := refluxTaskClaim(ctx, game.DB.Queries, commanderID, id, time.Now())
	if err != nil {
		return taskClaimed{}, false, err
	}
	if isReflux {
		drops, ok = refluxDrops, ok && unlocked
	}
	return taskClaimed{task: task, config: config, drops: drops}, ok, nil
}

// claimTasks submits claims in one unit of work: their drops, the commander
// manual progress they make, then the tasks they lead to (next_task), which
// start from the counters the claims updated and are pushed before the
// reply. pay, when not nil, runs first in the same unit of work (the
// vouchers of a quick finish); its error, a refusal too, is returned as is.
func (game *Game) claimTasks(ctx context.Context, client *connection.Client, claims []taskClaimed, pay func(tx *Tx) error) ([]*protobuf.DROPINFO, error) {
	now := uint32(time.Now().Unix())
	var awards []*protobuf.DROPINFO
	var next *protobuf.SC_20003
	var guildNotices []*guildNotices
	err := game.tx(ctx, client, func(tx *Tx) error {
		if pay != nil {
			if err := pay(tx); err != nil {
				return err
			}
		}
		var nextIDs []uint32
		for _, claim := range claims {
			if _, err := tx.SubmitTask(ctx, db.SubmitTaskParams{SubmitTime: now, CommanderID: client.CommanderID, TaskID: claim.task.TaskID}); err != nil {
				return err
			}
			if err := tx.refluxTaskClaimed(claim.task.TaskID); err != nil {
				return err
			}
			if cost := taskGiveCost(claim.config); cost != nil {
				if err := tx.payDrops([][]uint32{cost}); err != nil {
					return err
				}
			}
			drops := configDrops(claim.drops)
			guildDrops, notices, err := game.guildTaskClaimed(tx, claim.task.TaskID, claim.config)
			if err != nil {
				return err
			}
			drops, guildNotices = append(drops, guildDrops...), append(guildNotices, notices)
			if err := tx.grantDrops(drops); err != nil {
				return err
			}
			awards = append(awards, drops...)
			handbookEvents, err := handbookTaskClaimed(ctx, tx.Queries, client.CommanderID, claim.task.TaskID)
			if err != nil {
				return err
			}
			tx.event(handbookEvents...)
			nextIDs = append(nextIDs, claim.config.next...)
		}
		tx.afterEvents(func() (err error) {
			next, err = acceptTasks(ctx, tx.Queries, client.CommanderID, nextIDs, now)
			return err
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, notices := range guildNotices {
		game.tellGuildNotices(client, notices)
	}
	if next != nil {
		if err := client.Push(next); err != nil {
			return nil, err
		}
	}
	return awards, nil
}

// handleClientTaskEvent takes progress the client counts itself
// (MiniGameTaskProgressUpdateCommand: event_type is the task's sub_type, e.g.
// 2020 "visit the events page"), pushes it and replies.
func (game *Game) handleClientTaskEvent(client *connection.Client, req *protobuf.CS_20016) error {
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		tx.event(TaskEvent{SubType: req.GetEventType(), Target: req.GetEventTarget(), Count: req.GetEventCount()})
		return nil
	})
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_20017{Result: result(nil)})
}

// handleSubmitActivityTasks claims finished activity tasks
// (SubmitActiveTaskCommand, the client also sends it by itself for
// auto_commit ones): the progress the rewards cause, then the claimed tasks'
// removal (SC_20204), then the rewards.
func (game *Game) handleSubmitActivityTasks(client *connection.Client, req *protobuf.CS_20205) error {
	ctx := context.Background()
	activities, err := game.activityTaskLists(ctx, game.DB.Queries, client.CommanderID, time.Now())
	if err != nil {
		return err
	}
	var claimed []activityTask
	for _, activity := range activities {
		if activity.id != req.GetActId() {
			continue
		}
		for _, task := range activity.tasks {
			if slices.Contains(req.GetTaskIds(), task.taskID) {
				claimed = append(claimed, task)
			}
		}
	}
	if len(claimed) != len(req.GetTaskIds()) {
		return client.Send(&protobuf.SC_20206{Result: result(errRefused)})
	}
	for _, task := range claimed {
		if config, ok := taskConfigs[task.taskID]; !ok || task.finished || task.progress < config.TargetNum {
			return client.Send(&protobuf.SC_20206{Result: result(errRefused)})
		}
	}
	reply := &protobuf.SC_20206{Result: result(nil)}
	var removed *protobuf.ACT_TASK_LIST
	err = game.tx(ctx, client, func(tx *Tx) (err error) {
		reply.AwardList, removed, err = tx.finishActivityTasks(req.GetActId(), claimed)
		return err
	})
	if err != nil {
		return err
	}
	if err := client.Push(&protobuf.SC_20204{Info: []*protobuf.ACT_TASK_LIST{removed}}); err != nil {
		return err
	}
	return client.Send(reply)
}

// finishActivityTasks claims an activity's tasks: they are stored finished
// and their drops given. It returns the drops and the tasks as SC_20204
// lists them.
func (tx *Tx) finishActivityTasks(actID uint32, tasks []activityTask) ([]*protobuf.DROPINFO, *protobuf.ACT_TASK_LIST, error) {
	var drops []*protobuf.DROPINFO
	removed := &protobuf.ACT_TASK_LIST{ActId: proto.Uint32(actID)}
	for _, task := range tasks {
		if err := tx.FinishActivityTask(tx.ctx, db.FinishActivityTaskParams{CommanderID: tx.commanderID, ActID: actID, TaskID: task.taskID, Progress: task.progress}); err != nil {
			return nil, nil, err
		}
		awards := configDrops(taskConfigs[task.taskID].awards)
		if err := tx.grantDrops(awards); err != nil {
			return nil, nil, err
		}
		drops = append(drops, awards...)
		removed.Tasks = append(removed.Tasks, &protobuf.ACT_TASK{Id: proto.Uint32(task.taskID), Progress: proto.Uint32(task.progress)})
	}
	return drops, removed, nil
}

// openActivityTask is one of the commander's tasks of an activity running
// at now, false when the activity isn't running or hasn't the task.
func (game *Game) openActivityTask(ctx context.Context, commanderID, actID, taskID uint32, now time.Time) (activityTask, bool, error) {
	activities, err := game.activityTaskLists(ctx, game.DB.Queries, commanderID, now)
	if err != nil {
		return activityTask{}, false, err
	}
	for _, activity := range activities {
		if activity.id != actID {
			continue
		}
		for _, task := range activity.tasks {
			if task.taskID == taskID {
				return task, true, nil
			}
		}
	}
	return activityTask{}, false, nil
}

// syncStateTasks gives the commander manual's state tasks their current
// values: the engine only counts events, so a task about what the commander
// already has ("own a ship at level 10", "12 stars", "a ship broken out once")
// sits at 0 forever without this. Run at login and when the manual opens; it
// only ever raises progress, because the engine takes the event's value as the
// new progress for these sub types.
func (tx *Tx) syncStateTasks() error {
	tasks, err := tx.ListTasks(tx.ctx, tx.commanderID)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		return nil
	}
	ships, err := tx.ListShipsByCommander(tx.ctx, tx.commanderID)
	if err != nil {
		return err
	}
	var topLevel, topBreakout uint32
	for _, ship := range ships {
		topLevel = max(topLevel, ship.Level)
		// a limit break moves the ship to template group*10+n: n-1 breakouts
		if n := ship.TemplateID % 10; n > 1 {
			topBreakout = max(topBreakout, n-1)
		}
	}
	progress, err := tx.ListChapterProgress(tx.ctx, tx.commanderID)
	if err != nil {
		return err
	}
	var stars uint32
	for _, row := range progress {
		if row.Progress >= 100 {
			stars++
		}
		if row.KillBossCount > 0 {
			stars++
		}
		if row.TakeBoxCount > 0 {
			stars++
		}
	}
	for _, task := range tasks {
		config, ok := taskConfigs[task.TaskID]
		if !ok {
			continue
		}
		var value uint32
		switch config.SubType {
		case TaskSubShipLevel:
			value = min(topLevel, config.TargetNum)
		case TaskSubShipLevel2:
			if need := config.targetID2Value(); need > 0 && topLevel >= need {
				value = 1
			}
		case TaskSubBreakoutStar:
			value = min(topBreakout, config.TargetNum)
		case TaskSubStars:
			value = min(stars, config.TargetNum)
		default:
			continue
		}
		if value == 0 || value <= task.Progress {
			continue
		}
		tx.event(TaskEvent{SubType: config.SubType, Count: value})
	}
	return nil
}
