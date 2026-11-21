package game

import (
	"context"
	"maps"
	"math/rand/v2"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// Operation Siren's tasks (world_task_data, WorldTaskProxy / WorldTask):
// CS_33205 (WorldTriggerTaskCommand), CS_33207 (WorldSubmitTaskCommand and
// WorldAutoSubmitTaskCommand), CS_33413 / CS_33415 (the daily tasks,
// WorldTaskProxy:checkDailyTask, WorldTriggerDailyTaskCommand) and the
// SC_33203 / SC_33204 pushes. None of it was captured (the official
// account never reached the mode): built from the client's Lua and the
// tables alone, check on the device first.
//
// What a complete_condition counts is the server's; the tables and the
// client tell this much:
//   - 2 (WorldConst.TaskTypeSubmitItem): complete_parameter_num of the
//     world item complete_parameter[1] held, which the client counts
//     itself (WorldTask:Ctor, the item listeners) and takes back on submit
//     when item_retrieve is 1;
//   - 4: the task complete_parameter[1] accepted (the story chapters end
//     when an event hands out the next one, effect type 5);
//   - 6 (TaskTypeArrivePort): the fleet at the port (OpReqTask, sent by
//     WorldPortMediator:CheckTaskNotify);
//   - 7 (TaskTypeFleetExpansion): complete_parameter[1] fleets deployed
//     (the redeploy order of a port, CS_33409);
//   - 8: the triggers complete_parameter fired by an event's effect type 21
//     (every one of them is some world_effect_data type 21 parameter);
//   - 12 (TaskTypePressingMap): complete_parameter_num of the maps
//     complete_parameter secured, counted by the client too.
// A task stays in task_list once submitted (submite_time set: the client
// keeps it as "recycled", WorldTaskProxy:Setup).

// world_task_data complete_condition values.
const (
	worldTaskSubmitItem = 2
	worldTaskAccepted   = 4
	worldTaskArrivePort = 6
	worldTaskExpansion  = 7
	worldTaskTrigger    = 8
	worldTaskPressing   = 12
)

// worldSystemDailyTask is WorldConst.SystemDailyTask (world_stage_template
// stage_ui), the stage that opens the daily tasks.
const worldSystemDailyTask = 22

// worldTaskConfig is a world_task_data row.
type worldTaskConfig struct {
	Type                 uint32     `json:"type"`
	CompleteCondition    uint32     `json:"complete_condition"`
	CompleteParameter    []uint32   `json:"complete_parameter"`
	CompleteParameterNum uint32     `json:"complete_parameter_num"`
	CompleteStage        uint32     `json:"complete_stage"` // the progress the submit brings (World:UpdateProgress)
	ItemRetrieve         uint32     `json:"item_retrieve"`
	NextTask             []uint32   `json:"next_task"` // accepted once it is submitted
	FollowingMap         []uint32   `json:"following_map"`
	FollowingRandom      []uint32   `json:"following_random"`
	TaskRandomEnter      []uint32   `json:"task_random_enter"` // the entrances one is drawn from (event_map_id)
	TaskEvent            []uint32   `json:"task_event"`        // events added to its map once accepted
	Show                 [][]uint32 `json:"show"`              // {type, id, count}: its reward
	Exp                  uint32     `json:"exp"`
	Intimacy             uint32     `json:"intimacy"`
	NeedTaskComplete     uint32     `json:"need_task_complete"`
	ServerType           uint32     `json:"server_type"` // worldTaskServerPort: offered by the ports
}

// world_task_data server_type values: worldTaskServerPort the tasks the
// ports offer (portTasks, the "Investigate Siren Research Facility" ones),
// worldTaskServerCollection the collection file missions.
const (
	worldTaskServerPort       = 2
	worldTaskServerCollection = 4
)

// worldDailyPoolEntry is a world_daily_task_pool entry.
type worldDailyPoolEntry struct {
	task, weight, minStage, maxStage uint32
}

var (
	worldTasks map[uint32]worldTaskConfig
	// worldTaskEvents are the events some task's task_event adds
	worldTaskEvents map[uint32]bool

	worldTaskSettings struct {
		dailyPool  []worldDailyPoolEntry // world_daily_task_pool
		dailyNum   []uint32              // world_daily_task_num: dailies added a day, Monday first
		dailyLimit uint32                // world_daily_task_limit: dailies queued at most
		doingMax   uint32                // world_port_taskmax: tasks under way at once (the Mission Hub)
	}
)

// loadWorldTaskData reads world_task_data, world_target_data and the
// gameset values of the dailies.
func loadWorldTaskData() error {
	var pool [][4]uint32
	var gameset map[string]uint32
	err := gamedata.LoadAll(
		gamedata.Load("world_task_data", &worldTasks),
		loadWorldAchieveData,
		func() (err error) {
			gameset, err = gamedata.Gameset()
			return err
		},
		func() error { return gamedata.GamesetDescription("world_daily_task_pool", &pool) },
		func() error {
			return gamedata.GamesetDescription("world_daily_task_num", &worldTaskSettings.dailyNum)
		},
	)
	if err != nil {
		return err
	}
	for _, entry := range pool {
		if _, ok := worldTasks[entry[0]]; ok {
			worldTaskSettings.dailyPool = append(worldTaskSettings.dailyPool, worldDailyPoolEntry{entry[0], entry[1], entry[2], entry[3]})
		}
	}
	worldTaskEvents = map[uint32]bool{}
	for _, config := range worldTasks {
		for _, id := range config.TaskEvent {
			worldTaskEvents[id] = true
		}
	}
	worldTaskSettings.dailyLimit = gameset["world_daily_task_limit"]
	worldTaskSettings.doingMax = gameset["world_port_taskmax"]
	if len(worldTaskSettings.dailyNum) != 7 {
		return gamedata.Missing("gameset", "world_daily_task_num for each week day")
	}
	return nil
}

// worldDaily is the daily task queue (SC_33414).
type worldDaily struct {
	Tasks   []uint32 `json:"tasks,omitempty"` // queued, not accepted yet
	Refresh uint32   `json:"refresh,omitempty"`
}

// task is the task id of the world, nil for none.
func (state *worldState) task(id uint32) *worldTask {
	for i := range state.Tasks {
		if state.Tasks[i].ID == id {
			return &state.Tasks[i]
		}
	}
	return nil
}

// alive is WorldTask:isAlive: accepted and not submitted.
func (task *worldTask) alive() bool {
	return task.AcceptTime != 0 && task.SubmitTime == 0
}

func (task *worldTask) info() *protobuf.TASK_INFO {
	return &protobuf.TASK_INFO{
		Id:          proto.Uint32(task.ID),
		Progress:    proto.Uint32(task.Progress),
		AcceptTime:  proto.Uint32(task.AcceptTime),
		SubmiteTime: proto.Uint32(task.SubmitTime),
		EventMapId:  proto.Uint32(task.EventMapID),
	}
}

// doingTasks counts the tasks under way (getDoingTaskVOs).
func (state *worldState) doingTasks() uint32 {
	n := uint32(0)
	for i := range state.Tasks {
		if state.Tasks[i].alive() {
			n++
		}
	}
	return n
}

// taskProgress is what a task has done: the client's own count for the
// item and securing tasks, the counted one for the others.
func (state *worldState) taskProgress(task *worldTask) uint32 {
	config := worldTasks[task.ID]
	switch config.CompleteCondition {
	case worldTaskSubmitItem:
		if len(config.CompleteParameter) > 0 {
			return state.item(config.CompleteParameter[0])
		}
	case worldTaskPressing:
		n := uint32(0)
		for _, id := range config.CompleteParameter {
			if slices.Contains(state.CleanChapters, id) {
				n++
			}
		}
		return n
	}
	return task.Progress
}

// pickTaskEntrance draws a task's event_map_id from task_random_enter: the
// entrances whose base map the world's progress opens, any of them when
// none is open. The tasks of types 0, 6 and 7 follow following_map instead
// (WorldTask:GetFollowingEntrance).
func (state *worldState) pickTaskEntrance(config worldTaskConfig, rng *rand.Rand) uint32 {
	switch config.Type {
	case 0, 6, 7:
		return 0
	}
	var open, known []uint32
	for _, id := range config.TaskRandomEnter {
		mask, ok := worldColormasks[id]
		if !ok {
			continue
		}
		known = append(known, id)
		if random := worldRandomMaps[mask.Chapter]; len(random.OpenStage) == 0 || random.OpenStage[0] <= state.Count.Progress {
			open = append(open, id)
		}
	}
	if len(open) == 0 {
		open = known
	}
	if len(open) == 0 {
		return 0
	}
	return open[rng.IntN(len(open))]
}

// acceptWorldTask hands task id out (accept_time now), nil when there is no
// such task or it is already under way. A task submitted before starts
// over. Its task_event events are added to the map it leads to.
func (tx *Tx) acceptWorldTask(state *worldState, id uint32, now time.Time, rng *rand.Rand) *worldTask {
	config, ok := worldTasks[id]
	if !ok {
		return nil
	}
	task := state.task(id)
	if task != nil && task.alive() {
		return nil
	}
	if task == nil {
		state.Tasks = append(state.Tasks, worldTask{ID: id})
		task = &state.Tasks[len(state.Tasks)-1]
	}
	*task = worldTask{ID: id, AcceptTime: uint32(now.Unix()), EventMapID: state.pickTaskEntrance(config, rng)}
	if len(config.TaskEvent) > 0 {
		tx.spawnTaskEvents(state, config, task.EventMapID, rng, true)
	}
	return state.task(id)
}

// spawnTaskEvents adds a task's task_event events to the map it leads to
// (following_random, else its entrance's base map) unless the map has them
// already. It runs once the request's other changes are saved (a map
// operation saves its maps last), the active map's new cells pushed (push) in an
// SC_33105. Storing the map doesn't put it in chapter_list: entering it
// still costs its enter_cost. Where the official server puts them isn't known: their grids
// are read like the effects' spawns.
func (tx *Tx) spawnTaskEvents(state *worldState, config worldTaskConfig, entrance uint32, rng *rand.Rand, push bool) {
	randomID := uint32(0)
	if len(config.FollowingRandom) > 0 {
		randomID = config.FollowingRandom[0]
	}
	if randomID == 0 {
		if entrance == 0 && len(config.FollowingMap) > 0 {
			entrance = config.FollowingMap[0]
		}
		randomID = worldColormasks[entrance].Chapter
	}
	if _, ok := worldRandomMaps[randomID]; !ok || !state.activated() {
		return
	}
	events := slices.Clone(config.TaskEvent)
	tx.afterEvents(func() error {
		m, err := tx.worldMap(state, randomID)
		if err != nil || m == nil {
			return err
		}
		template := worldTemplates[m.TemplateID]
		if template == nil {
			return nil
		}
		var fleets []*worldFleet
		if m.RandomID == state.MapID {
			fleets = state.Fleets
		}
		var added []*worldAttachment
		for _, id := range events {
			if _, ok := worldEvents[id]; !ok || slices.ContainsFunc(m.Attachments, func(a *worldAttachment) bool {
				return a.Type == worldAttachEvent && a.ID == id
			}) {
				continue
			}
			added = append(added, spawnWorldEvent(m, template, id, fleets, rng)...)
		}
		if len(added) == 0 {
			return nil
		}
		if err := tx.saveWorldMap(m); err != nil {
			return err
		}
		if push && m.RandomID == state.MapID {
			cells := &protobuf.SC_33105{}
			for _, a := range added {
				cells.PosList = append(cells.PosList, m.posInfo(a.Row, a.Column, state.Fleets))
			}
			tx.push(cells)
		}
		return nil
	})
}

// respawnTaskEvents puts back the task_event events of the tasks under way
// on maps that lack them: the monthly reset drops the maps but keeps the
// Siren research and collection missions, whose events are on them
// (spawnTaskEvents skips the maps that have them already). Run by the
// activation, whose reply brings the active map anyway: nothing is pushed.
func (tx *Tx) respawnTaskEvents(state *worldState, now time.Time) {
	rng := rand.New(rand.NewPCG(uint64(now.UnixNano()), uint64(tx.commanderID)))
	for _, task := range state.Tasks {
		if config := worldTasks[task.ID]; task.alive() && len(config.TaskEvent) > 0 {
			tx.spawnTaskEvents(state, config, task.EventMapID, rng, false)
		}
	}
}

// recountWorldTasks counts what happened for the tasks under way; it
// returns the ones whose progress the client has to be told.
func (state *worldState) recountWorldTasks(happened *worldHappenings) []uint32 {
	var changed []uint32
	normalFleets := uint32(0)
	for _, fleet := range state.Fleets {
		if !fleet.Submarine {
			normalFleets++
		}
	}
	for i := range state.Tasks {
		task := &state.Tasks[i]
		config, ok := worldTasks[task.ID]
		if !ok || !task.alive() {
			continue
		}
		target := config.CompleteParameterNum
		progress := task.Progress
		switch config.CompleteCondition {
		case worldTaskSubmitItem, worldTaskPressing:
			// the client counts them itself
			task.Progress = state.taskProgress(task)
			continue
		case worldTaskAccepted:
			n := uint32(0)
			for _, id := range config.CompleteParameter {
				if other := state.task(id); other != nil && other.AcceptTime != 0 {
					n++
				}
			}
			progress = max(progress, min(n, target))
		case worldTaskArrivePort:
			if happened.PortTasks && slices.Contains(config.CompleteParameter, state.activePort()) {
				progress = target
			}
		case worldTaskExpansion:
			if len(config.CompleteParameter) > 0 && normalFleets >= config.CompleteParameter[0] {
				progress = target
			}
		case worldTaskTrigger:
			for _, trigger := range happened.Triggers {
				if slices.Contains(config.CompleteParameter, trigger) && progress < target {
					progress++
				}
			}
		}
		if progress != task.Progress {
			task.Progress = progress
			changed = append(changed, task.ID)
		}
	}
	return changed
}

// worldStoryGaps are the story tasks whose handing-out events are not on any
// map in the data: 2010/2110/2210's 20900/21900/22900, 2103's 21202 and
// 2304's 23301 are in world_event_data but no chapter or map references
// them, so nothing on the maps ever accepts those tasks. Submitting the task
// before them hands them out instead, letting the story chain run to the
// progress 100 that opens the daily task board (world_stage_template 20,
// WorldConst.SystemDailyTask 22).
var worldStoryGaps = [][2]uint32{
	{2050, 2000}, // Chapter1-1 -> Chapter1-2 (event 20281 grid out of bounds)
	{2004, 2010}, // Chapter1-6 -> Chapter1-SP
	{2102, 2103}, // Chapter2-3 -> Chapter2-4
	{2105, 2110}, // Chapter2-6 -> Chapter2-SP
	{2205, 2210}, // Chapter3-6 -> Chapter3-SP
	{2303, 2304}, // Chapter4-4 -> Chapter4-5
	{2305, 2310}, // Chapter4-6 -> Chapter4-SP
}

// worldTaskFollowups are the tasks a story task hands out through a story
// event chain the maps don't carry: 2100 (Chapter2-1 集结) spawns event
// 20901 and the 21010/21020 chain accepts 2150 (扩编舰队), but no map has
// that chain, so 2-1 never completes and its port stays locked. Accept the
// follow-up once its source is under way.
var worldTaskFollowups = [][2]uint32{
	{2100, 2150},
}

// acceptFollowupTask hands out the follow-up task of a task under way that
// no event on the maps gives: nil when none is due.
func (tx *Tx) acceptFollowupTask(state *worldState, now time.Time, rng *rand.Rand) *worldTask {
	if !state.activated() {
		return nil
	}
	for _, pair := range worldTaskFollowups {
		source := state.task(pair[0])
		if source == nil || !source.alive() {
			continue
		}
		next := state.task(pair[1])
		if next != nil && (next.SubmitTime != 0 || next.alive()) {
			continue
		}
		config, ok := worldTasks[pair[1]]
		if !ok || state.TaskFinishCount < config.NeedTaskComplete {
			continue
		}
		task := tx.acceptWorldTask(state, pair[1], now, rng)
		if task != nil && pair[1] == 2150 {
			// the same missing 21010/21020 chain raises the progress 25 -> 30
			// (effect 210202), the stage that allows a fourth fleet
			// (world_stage_template 8, fleet_num 4)
			state.Count.Progress = max(state.Count.Progress, 30)
		}
		return task
	}
	return nil
}

// reconcileWorldProgress applies the progress a missing story chain would
// have raised: 2150's 21010/21020 chain raises 25 -> 30 (effect 210202), the
// stage that allows a fourth fleet (world_stage_template 8, fleet_num 4).
// Without it the fleet expansion task can never be done.
func reconcileWorldProgress(state *worldState) bool {
	if !state.activated() || state.Count.Progress >= 30 {
		return false
	}
	if task := state.task(2150); task != nil && (task.alive() || task.SubmitTime != 0) {
		state.Count.Progress = 30
		return true
	}
	return false
}

// acceptStoryGapTask hands out the story task no event on the maps gives,
// once the task before it is submitted: nil when none is due.
func (tx *Tx) acceptStoryGapTask(state *worldState, now time.Time, rng *rand.Rand) *worldTask {
	if !state.activated() {
		return nil
	}
	for _, pair := range worldStoryGaps {
		previous := state.task(pair[0])
		if previous == nil || previous.SubmitTime == 0 {
			continue
		}
		next := state.task(pair[1])
		if next != nil && (next.SubmitTime != 0 || next.alive()) {
			continue
		}
		config, ok := worldTasks[pair[1]]
		if !ok || state.TaskFinishCount < config.NeedTaskComplete {
			continue
		}
		return tx.acceptWorldTask(state, pair[1], now, rng)
	}
	return nil
}

// reconcileStoryUnlocks applies what the missing 2-1 story chain would have
// marked: the story happens at 利维浦 (zone 2), and that zone is the only
// border of 122 (东大陆架B) that leads into NA海域东北. As the chain never
// ran, 利维浦 stayed unpressed, so 122's border (2) stayed unsuppressed and
// every chapter 2 map (132 etc.) stayed blocked. Once 2150 is submitted
// (2-1 done), mark the story's zone pressed.
func reconcileStoryUnlocks(state *worldState) bool {
	if !state.activated() {
		return false
	}
	task := state.task(2150)
	if task == nil || task.SubmitTime == 0 || slices.Contains(state.CleanChapters, 2) {
		return false
	}
	state.CleanChapters = append(state.CleanChapters, 2)
	return true
}

// worldTasksUpdated is the world tasks' hook, called once per map operation
// (CS_33103) and world battle, and after a redeploy: the tasks effects
// type 5 hand out are accepted, what happened is counted, and the client
// is told in an SC_33203 (tasks new or changed: WorldProxy adds or updates
// them, the finished auto_complete ones it then submits itself) and an
// SC_33204 (the tasks effect type 40 removed from state.Tasks, which the
// client deletes too), ahead of the reply.
func (tx *Tx) worldTasksUpdated(state *worldState, happened *worldHappenings) error {
	now := serverNow()
	rng := rand.New(rand.NewPCG(uint64(now.UnixNano()), uint64(tx.commanderID)))
	var updated []uint32
	for _, id := range happened.Accepted {
		if task := tx.acceptWorldTask(state, id, now, rng); task != nil && !slices.Contains(updated, id) {
			updated = append(updated, id)
		}
	}
	for _, id := range state.recountWorldTasks(happened) {
		if !slices.Contains(updated, id) {
			updated = append(updated, id)
		}
	}
	if task := tx.acceptStoryGapTask(state, now, rng); task != nil && !slices.Contains(updated, task.ID) {
		updated = append(updated, task.ID)
	}
	if task := tx.acceptFollowupTask(state, now, rng); task != nil && !slices.Contains(updated, task.ID) {
		updated = append(updated, task.ID)
	}
	reconcileWorldProgress(state)
	if len(happened.Deleted) > 0 {
		var deleted []uint32
		for _, id := range happened.Deleted {
			if !slices.Contains(deleted, id) && state.task(id) == nil {
				deleted = append(deleted, id)
			}
		}
		tx.push(&protobuf.SC_33204{DeleteList: deleted})
	}
	if len(updated) > 0 {
		tx.push(state.taskUpdates(updated))
	}
	return nil
}

// taskUpdates is the SC_33203 of the tasks ids.
func (state *worldState) taskUpdates(ids []uint32) *protobuf.SC_33203 {
	push := &protobuf.SC_33203{}
	for _, id := range ids {
		if task := state.task(id); task != nil {
			push.UpdateList = append(push.UpdateList, task.info())
		}
	}
	return push
}

// portTasks are the tasks a port offers (PORT_INFO.task_list, the port's
// task tab, WorldPortLayer:UpdateTasks). No table names a port's tasks:
// world_task_data's server_type groups them, 1 the story chapters
// (handed out by events), 3 the dailies (world_daily_task_pool) and the
// Arbiter tasks, 4 the collection file missions, and 2 the twelve "Siren
// Research Facility" investigations (type 5, task_goto 3) that nothing
// else hands out, their priority falling region by region as the regions
// open (9 for the first, 6 for the last): those are the ports'. Every port
// of a realm (port_camp not 0) offers the ones whose map (following_map's
// base map) the progress opens, never one the world has had
// (WorldTask.canTrigger refuses those: "world_sametask_tip").
func (state *worldState) portTasks(portID uint32) []uint32 {
	if worldPorts[portID].PortCamp == 0 {
		return nil
	}
	var ids []uint32
	for _, id := range slices.Sorted(maps.Keys(worldTasks)) {
		config := worldTasks[id]
		if config.ServerType != worldTaskServerPort || state.task(id) != nil || len(config.FollowingMap) == 0 {
			continue
		}
		mask, ok := worldColormasks[config.FollowingMap[0]]
		if !ok {
			continue
		}
		if random := worldRandomMaps[mask.Chapter]; len(random.OpenStage) > 0 && random.OpenStage[0] > state.Count.Progress {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

// handleWorldTriggerTask answers WorldTriggerTaskCommand (CS_33205): a task
// of the active map's port is accepted (task, WorldTask.New; the client
// takes it off the port's task_list). WorldTask.canTrigger checked it first
// (not under way, need_task_complete); result 6 is "world_task_refuse1",
// too many tasks under way (world_port_taskmax). The task must be one the
// active map's port offers (portTasks).
func (game *Game) handleWorldTriggerTask(client *connection.Client, req *protobuf.CS_33205) error {
	ctx := context.Background()
	now := serverNow()
	var task *protobuf.TASK_INFO
	err := game.tx(ctx, client, func(tx *Tx) error {
		state, err := tx.worldState(now)
		if err != nil {
			return err
		}
		config, ok := worldTasks[req.GetTaskId()]
		port := state.port(state.activePort())
		if !ok || port == nil || !slices.Contains(state.portTasks(port.PortID), req.GetTaskId()) || state.TaskFinishCount < config.NeedTaskComplete {
			return errRefused
		}
		if state.doingTasks() >= worldTaskSettings.doingMax {
			return refusal{"too many world tasks", 6}
		}
		rng := rand.New(rand.NewPCG(uint64(now.UnixNano()), uint64(tx.commanderID)))
		accepted := tx.acceptWorldTask(state, req.GetTaskId(), now, rng)
		if accepted == nil {
			return errRefused
		}
		port.Tasks = slices.DeleteFunc(port.Tasks, func(id uint32) bool { return id == req.GetTaskId() })
		task = accepted.info()
		return tx.saveWorldState(state)
	})
	if refused(err) {
		return client.Send(&protobuf.SC_33206{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_33206{Result: result(nil), Task: task})
}

// handleWorldSubmitTask answers WorldSubmitTaskCommand and
// WorldAutoSubmitTaskCommand (CS_33207): a finished task is submitted. The
// client then marks it submitted, counts it (task_finish_count), raises
// the progress to complete_stage, takes back the items of an item task
// with item_retrieve, adds drops (PlayerConst.addTranDrop) and adds exp
// and intimacy to every deployed ship (no task gives any). The reward is
// the task's show list (its drop id is a server-side table). The tasks of
// next_task are accepted, in an SC_33203 after the reply. Errors show
// errorTip("task_submitTask", result).
func (game *Game) handleWorldSubmitTask(client *connection.Client, req *protobuf.CS_33207) error {
	ctx := context.Background()
	now := serverNow()
	reply := &protobuf.SC_33208{Result: result(nil), Exp: proto.Uint32(0), Intimacy: proto.Uint32(0)}
	var next *protobuf.SC_33203
	err := game.tx(ctx, client, func(tx *Tx) error {
		state, err := tx.worldState(now)
		if err != nil {
			return err
		}
		task := state.task(req.GetTaskId())
		config, ok := worldTasks[req.GetTaskId()]
		if !ok || task == nil || !task.alive() {
			return errRefused
		}
		progress := state.taskProgress(task)
		if progress < config.CompleteParameterNum {
			logger.WithFields("World", logger.FieldValue("task", task.ID), logger.FieldValue("progress", progress), logger.FieldValue("target", config.CompleteParameterNum)).Warn("task submit refused")
			return errRefused
		}
		task.Progress = progress
		task.SubmitTime = uint32(now.Unix())
		state.TaskFinishCount++
		state.Count.Progress = max(state.Count.Progress, config.CompleteStage)
		if config.CompleteCondition == worldTaskSubmitItem && config.ItemRetrieve == 1 && len(config.CompleteParameter) > 0 {
			state.addItem(config.CompleteParameter[0], -int64(config.CompleteParameterNum))
		}
		reply.Drops = configDrops(config.Show)
		reply.Exp = proto.Uint32(config.Exp)
		reply.Intimacy = proto.Uint32(config.Intimacy)
		if err := tx.grantWorldDrops(state, reply.Drops); err != nil {
			return err
		}
		rng := rand.New(rand.NewPCG(uint64(now.UnixNano()), uint64(tx.commanderID)))
		var ids []uint32
		for _, id := range config.NextTask {
			if tx.acceptWorldTask(state, id, now, rng) != nil {
				ids = append(ids, id)
			}
		}
		// a next task may be done already (an item task)
		ids = append(ids, state.recountWorldTasks(&worldHappenings{})...)
		if task := tx.acceptStoryGapTask(state, now, rng); task != nil {
			ids = append(ids, task.ID)
		}
		if len(ids) > 0 {
			next = state.taskUpdates(ids)
		}
		return tx.saveWorldState(state)
	})
	if refused(err) {
		return client.Send(&protobuf.SC_33208{Result: result(err), Exp: proto.Uint32(0), Intimacy: proto.Uint32(0)})
	}
	if err != nil {
		return err
	}
	if err := client.Send(reply); err != nil {
		return err
	}
	if next != nil {
		return client.PushAfterReply(next)
	}
	return nil
}

// dailyOffer is the daily task queue at now. Every server midnight adds
// world_daily_task_num[week day] tasks (5 on weekdays, 6 at the weekend)
// to the queue, which holds world_daily_task_limit (12) at most: once full
// no more are added (the wiki's Operation Siren page: "Up to 12 daily
// missions can be queued, after which new daily missions will not spawn").
// They are drawn from world_daily_task_pool ({task, weight, lowest
// progress, highest progress}) by weight, among those the progress is in
// range of, neither queued nor under way; none before the daily tasks open
// (SystemDailyTask). The days the world wasn't asked about add theirs too
// (at most until the queue is full); the first ask adds the day's only.
// How the official server draws them isn't known.
func (state *worldState) dailyOffer(commanderID uint32, now time.Time) {
	daily := &state.Daily
	if daily.Refresh > uint32(now.Unix()) {
		return
	}
	day := startOfDay(now)
	if daily.Refresh != 0 {
		day = startOfDay(time.Unix(int64(daily.Refresh), 0))
	}
	daily.Refresh = uint32(nextDay(now).Unix())
	progress := state.Count.Progress
	if !state.activated() || !worldSystemOpen(worldSystemDailyTask, progress) {
		return
	}
	for ; !day.After(now) && uint32(len(daily.Tasks)) < worldTaskSettings.dailyLimit; day = day.AddDate(0, 0, 1) {
		var pool []worldDailyPoolEntry
		for _, entry := range worldTaskSettings.dailyPool {
			task := state.task(entry.task)
			if entry.minStage <= progress && progress <= entry.maxStage && (task == nil || !task.alive()) && !slices.Contains(daily.Tasks, entry.task) {
				pool = append(pool, entry)
			}
		}
		rng := rand.New(rand.NewPCG(uint64(commanderID), uint64(day.Unix())))
		for n := worldTaskSettings.dailyNum[serverWeekday(day)-1]; n > 0 && len(pool) > 0 && uint32(len(daily.Tasks)) < worldTaskSettings.dailyLimit; n-- {
			i := weightedPick(func(n uint32) uint32 { return rng.Uint32N(n) }, pool, func(e worldDailyPoolEntry) uint32 { return e.weight })
			if i < 0 {
				break
			}
			daily.Tasks = append(daily.Tasks, pool[i].task)
			pool = slices.Delete(pool, i, i+1)
		}
	}
}

// handleWorldDailyTasks answers WorldTaskProxy:checkDailyTask (CS_33413,
// type 0, sent when the daily task panel opens and its refresh time has
// passed): the daily tasks offered (task_list, UpdateDailyTaskIds) until
// next_refresh_time, which must be in the future (the client asserts it
// and asks again then).
func (game *Game) handleWorldDailyTasks(client *connection.Client, req *protobuf.CS_33413) error {
	ctx := context.Background()
	now := serverNow()
	reply := &protobuf.SC_33414{Result: result(nil)}
	err := game.tx(ctx, client, func(tx *Tx) error {
		state, err := tx.worldState(now)
		if err != nil {
			return err
		}
		state.dailyOffer(tx.commanderID, now)
		reply.TaskList = state.Daily.Tasks
		reply.NextRefreshTime = proto.Uint32(state.Daily.Refresh)
		return tx.saveWorldState(state)
	})
	if err != nil {
		return err
	}
	return client.Send(reply)
}

// handleWorldTriggerDailyTask answers WorldTriggerDailyTaskCommand
// (CS_33415): daily tasks of the offer are accepted (the panel's accept
// button, or accept all); the client adds task_list and takes them off
// its queue. Result 6 is "world_task_refuse1" (the Mission Hub full:
// world_port_taskmax tasks under way, as WorldTaskProxy:canAcceptDailyTask
// counts them), 20 "world_sametask_tip" (one is under way already).
// Accepting all stops once the hub is full, refused only when none could
// be.
func (game *Game) handleWorldTriggerDailyTask(client *connection.Client, req *protobuf.CS_33415) error {
	ctx := context.Background()
	now := serverNow()
	reply := &protobuf.SC_33416{Result: result(nil)}
	err := game.tx(ctx, client, func(tx *Tx) error {
		state, err := tx.worldState(now)
		if err != nil {
			return err
		}
		state.dailyOffer(tx.commanderID, now)
		rng := rand.New(rand.NewPCG(uint64(now.UnixNano()), uint64(tx.commanderID)))
		var refusedWith error = errRefused
		for _, id := range req.GetTaskList() {
			if !slices.Contains(state.Daily.Tasks, id) {
				continue
			}
			if task := state.task(id); task != nil && task.alive() {
				refusedWith = refusal{"daily task under way", 20}
				continue
			}
			if state.doingTasks() >= worldTaskSettings.doingMax {
				refusedWith = refusal{"too many daily tasks", 6}
				break
			}
			task := tx.acceptWorldTask(state, id, now, rng)
			if task == nil {
				continue
			}
			state.Daily.Tasks = slices.DeleteFunc(state.Daily.Tasks, func(t uint32) bool { return t == id })
			reply.TaskList = append(reply.TaskList, task.info())
		}
		if len(reply.TaskList) == 0 {
			return refusedWith
		}
		return tx.saveWorldState(state)
	})
	if refused(err) {
		return client.Send(&protobuf.SC_33416{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}
