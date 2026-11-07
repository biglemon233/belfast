package game

import (
	"cmp"
	"encoding/json"
	"maps"
	"slices"

	"github.com/ggmolly/belfast/internal/gamedata"
)

// task_data_template row (TaskProxy, ActivityTaskProxy tasks)
type taskConfig struct {
	Type         uint32          `json:"type"`
	Level        uint32          `json:"level"`
	IsHead       uint32          `json:"is_head"` // first task of its chain
	SubType      uint32          `json:"sub_type"`
	TargetID     json.RawMessage `json:"target_id"`
	// the second target: some sub types keep their threshold here as a string
	// (task 22012's "10" for "own a ship at level 10 or above")
	TargetID2    json.RawMessage `json:"target_id_2"`
	TargetNum    uint32          `json:"target_num"`
	AwardDisplay json.RawMessage `json:"award_display"`
	AwardChoice  json.RawMessage `json:"award_choice"`
	NextTask     json.RawMessage `json:"next_task"`
	AutoCommit   uint32          `json:"auto_commit"`
	// tasks to claim before it can be accepted (TaskProxy:isFinishPrevTasks)
	OpenNeed json.RawMessage `json:"open_need"`
	// cruise mission vouchers (QUICK_TASK_PASS_TICKET_ID) finishing the
	// task at once costs, 0 when it can't be (QuickTaskCommand)
	QuickFinish uint32 `json:"quick_finish"`
	// the server-only drop it gives, award_display showing what's in it
	Award uint32 `json:"award"`

	awards [][]uint32 // award_display
	target taskTarget // target_id
	next   []uint32   // next_task
}

// targetID2Value is task_data_template.target_id_2 as a number: the table
// stores it as a string ("10") or a number (0).
func (config taskConfig) targetID2Value() uint32 {
	if len(config.TargetID2) == 0 {
		return 0
	}
	var n uint32
	if json.Unmarshal(config.TargetID2, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(config.TargetID2, &s) != nil {
		return 0
	}
	var v uint32
	for _, r := range s {
		if r < '0' || r > '9' {
			return v
		}
		v = v*10 + uint32(r-'0')
	}
	return v
}

// weekly_task_template row (WeekPtTask)
type weeklyTaskConfig struct {
	ID        uint32          `json:"id"`
	Level     uint32          `json:"level"`
	SubType   uint32          `json:"sub_type"`
	TargetID  json.RawMessage `json:"target_id"`
	TargetNum uint32          `json:"target_num"`
	// one drop, the weekly mission points: [8, 59012, n] (WeekPtTask.GetAward)
	AwardDisplay []uint32 `json:"award_display"`

	target taskTarget // target_id
}

// activity_template row, only what task activities need
type activityConfig struct {
	ID         uint32          `json:"id"`
	Type       uint32          `json:"type"`
	ConfigID   uint32          `json:"config_id"`
	ConfigData json.RawMessage `json:"config_data"`
	Time       gamedata.Window `json:"time"`

	// for the activities with tasks (taskActivityTypes, task lists)
	tasks []uint32 // config_data
}

// battlepass_task_group row: the cruise pass tasks of one week (group_mask),
// or always open when is_ontime is 0
type battlepassTaskGroup struct {
	GroupMask uint32     `json:"group_mask"`
	IsOntime  uint32     `json:"is_ontime"`
	TaskGroup [][]uint32 `json:"task_group"`

	tasks []uint32 // task_group flattened, backwards (as the capture lists them)
}

// tutorial_handbook_task row: a commander manual page; task_list holds
// {tasks, tasks to finish before they are given} pairs, target the pt each
// pt reward needs and drop_client what each one gives
type handbookPageConfig struct {
	ID         uint32       `json:"id"`
	TaskList   [][][]uint32 `json:"task_list"`
	Unlock     []uint32     `json:"unlock"`
	Target     []uint32     `json:"target"`
	DropClient [][][]uint32 `json:"drop_client"`
}

var (
	taskConfigs          map[uint32]taskConfig
	weeklyTaskConfigs    map[uint32]weeklyTaskConfig
	activityConfigs      map[uint32]activityConfig
	battlepassTaskGroups map[uint32]battlepassTaskGroup
	handbookPageConfigs  map[uint32]handbookPageConfig

	// taskActivities are the activity_template rows of taskActivityTypes, by id
	taskActivities []activityConfig
	// task_data_template / weekly_task_template ids by sub_type
	taskIDsBySubType       map[uint32][]uint32
	weeklyTaskIDsBySubType map[uint32][]uint32

	// handbookTaskPages are the commander manual pages of each task, by page id
	handbookTaskPages map[uint32][]handbookTaskPage
	// handbookUnlockTasks are the tasks that unlock pages
	// (tutorial_handbook_task.unlock), sorted
	handbookUnlockTasks []uint32

	// weeklyBonusTasks is how many TYPE_NEW_WEEKLY tasks a commander is
	// offered (gameset weekly_bonus_task, server side: no Lua reads it; their
	// names end in "奖励任务", bonus task, and the capture has 2)
	weeklyBonusTasks uint32

	// weeklyFixedTasks are the TYPE_NEW_WEEKLY tasks every commander open
	// to them gets each week besides the draw (gameset weekly_fixed_task:
	// 7311, the Private Quarters' "Interact with shipgirls ... for 3
	// days", which SelectDorm3DScene reads unchecked)
	weeklyFixedTasks []uint32

	// weeklyTargets are the weekly mission points each phase of the weekly
	// reward bar needs (gameset weekly_target, WeekTaskProgress.targets), and
	// weeklyPhaseDrops what each phase gives (gameset weekly_drop_client,
	// WeekTaskProgress.dropData). The server's own gameset weekly_drop names
	// drop ids (7400-7419) of a table the client doesn't have, so the
	// rewards the client shows are the ones given.
	weeklyTargets    []uint32
	weeklyPhaseDrops [][][]uint32
)

// handbookTaskPage is a commander manual page a task belongs to: as one of
// its tasks (task_list[i][0]), or as a task to finish to unlock the page or
// its tasks (unlock, task_list[i][1]).
type handbookTaskPage struct {
	page     uint32
	pageTask bool
}

// loadTaskData reads the tasks, weekly missions, activities, cruise pass
// task groups and commander manual pages.
func loadTaskData() error {
	var gameset map[string]uint32
	err := gamedata.LoadAll(
		gamedata.Load("task_data_template", &taskConfigs),
		gamedata.Load("weekly_task_template", &weeklyTaskConfigs),
		gamedata.Load("activity_template", &activityConfigs),
		gamedata.Load("battlepass_task_group", &battlepassTaskGroups),
		loadCruiseData,
		loadEventPtData,
		gamedata.Load("tutorial_handbook_task", &handbookPageConfigs),
		gamedata.Load("fleet_tech_ship_template", &fleetTechShips),
		func() (err error) {
			gameset, err = gamedata.Gameset()
			return err
		},
		func() error { return gamedata.GamesetDescription("weekly_target", &weeklyTargets) },
		func() error { return gamedata.GamesetDescription("weekly_drop_client", &weeklyPhaseDrops) },
		func() error { return gamedata.GamesetDescription("weekly_fixed_task", &weeklyFixedTasks) },
	)
	if err != nil {
		return err
	}
	var ok bool
	if weeklyBonusTasks, ok = gameset["weekly_bonus_task"]; !ok {
		return gamedata.Missing("gameset", "weekly_bonus_task")
	}
	taskIDsBySubType = map[uint32][]uint32{}
	for id, config := range taskConfigs {
		// a task whose award_display isn't a drop list (one is a Lua
		// string) is left out like any row that doesn't fit
		if json.Unmarshal(config.AwardDisplay, &config.awards) != nil {
			delete(taskConfigs, id)
			continue
		}
		config.target = parseTaskTarget(config.TargetID)
		config.next = gamedata.IDs(config.NextTask)
		taskConfigs[id] = config
		taskIDsBySubType[config.SubType] = append(taskIDsBySubType[config.SubType], id)
	}
	weeklyTaskIDsBySubType = map[uint32][]uint32{}
	for id, config := range weeklyTaskConfigs {
		if len(config.AwardDisplay) < 3 {
			delete(weeklyTaskConfigs, id)
			continue
		}
		config.target = parseTaskTarget(config.TargetID)
		weeklyTaskConfigs[id] = config
		weeklyTaskIDsBySubType[config.SubType] = append(weeklyTaskIDsBySubType[config.SubType], id)
	}
	taskActivities = nil
	for id, config := range activityConfigs {
		if !taskActivityTypes[config.Type] && config.Type != activityTypeTaskList {
			continue
		}
		config.tasks = gamedata.IDs(config.ConfigData)
		activityConfigs[id] = config
		if taskActivityTypes[config.Type] {
			taskActivities = append(taskActivities, config)
		}
	}
	slices.SortFunc(taskActivities, func(a, b activityConfig) int { return cmp.Compare(a.ID, b.ID) })
	indexHandbookTasks()
	for id, group := range battlepassTaskGroups {
		for _, chain := range group.TaskGroup {
			group.tasks = append(group.tasks, chain...)
		}
		slices.Reverse(group.tasks)
		battlepassTaskGroups[id] = group
	}
	return nil
}

// indexHandbookTasks builds handbookTaskPages and handbookUnlockTasks.
func indexHandbookTasks() {
	pageTasks := map[uint32]map[uint32]bool{} // task -> page -> is a page task
	add := func(task, page uint32, pageTask bool) {
		if pageTasks[task] == nil {
			pageTasks[task] = map[uint32]bool{}
		}
		pageTasks[task][page] = pageTasks[task][page] || pageTask
	}
	handbookUnlockTasks = nil
	for id, page := range handbookPageConfigs {
		for _, task := range page.Unlock {
			add(task, id, false)
			if !slices.Contains(handbookUnlockTasks, task) {
				handbookUnlockTasks = append(handbookUnlockTasks, task)
			}
		}
		for _, entry := range page.TaskList {
			if len(entry) > 0 {
				for _, task := range entry[0] {
					add(task, id, true)
				}
			}
			if len(entry) > 1 {
				for _, task := range entry[1] {
					add(task, id, false)
				}
			}
		}
	}
	slices.Sort(handbookUnlockTasks)
	handbookTaskPages = make(map[uint32][]handbookTaskPage, len(pageTasks))
	for task, pages := range pageTasks {
		for _, page := range slices.Sorted(maps.Keys(pages)) {
			handbookTaskPages[task] = append(handbookTaskPages[task], handbookTaskPage{page: page, pageTask: pages[page]})
		}
	}
}

// taskTarget is a task's target_id: "0" / "" / [] for any target, an id, a
// list of ids, or a [">=", n] threshold.
type taskTarget struct {
	any bool
	ids []uint32
	min *uint32
}

func parseTaskTarget(raw json.RawMessage) taskTarget {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		ids := gamedata.IDs(raw)
		return taskTarget{any: len(ids) == 0, ids: ids}
	}
	var op string
	var number uint32
	if len(list) == 2 && json.Unmarshal(list[0], &op) == nil && json.Unmarshal(list[1], &number) == nil {
		return taskTarget{min: &number}
	}
	// only the list's own ids: nested lists ([[ship type, count], ...] of
	// the sortie exp tasks) name no target, they count for any
	var ids []uint32
	for _, item := range list {
		if json.Unmarshal(item, &number) == nil {
			ids = append(ids, number)
		}
	}
	return taskTarget{any: len(ids) == 0, ids: ids}
}

func (target taskTarget) matches(id uint32) bool {
	switch {
	case target.any:
		return true
	case target.min != nil:
		return id >= *target.min
	}
	return slices.Contains(target.ids, id)
}
