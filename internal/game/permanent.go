package game

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// Mini-events replayed from the Mini-Event Gallery (the Events screen's
// "activity_permanent" entrance, ActivityPermanentLayer) and the boss rush
// archives started from the War Archives (LevelRemasterView): the
// activity_template rows of activity_task_permanent, whose time is "stop".
// The commander runs them at their own time: started, they are the
// commander's activities until finished or stopped (ActivityPermanentProxy).

// ActivityPermanentProxy.TYPE_*: activity_task_permanent activity_group
// ranges (ACTIVITY_GROUP_RANGES)
const (
	permanentTypeNormal   = 1 // mini-events, groups 1-1999
	permanentTypeRemaster = 2 // boss rush archives, groups 2000-2999
)

// CS_11208 typ
const (
	permanentFinish = 1 // ActivityPermanentFinishCommand
	permanentStop   = 2 // ActivityPermanentStopCommand
)

// activity_task_permanent row: eliminate_task_id are the activity's tasks
// the server takes back when it is stopped or finished (no Lua reads it)
type permanentActivityConfig struct {
	ActivityGroup   uint32          `json:"activity_group"`
	EliminateTaskID json.RawMessage `json:"eliminate_task_id"`

	eliminate []uint32
}

// activity_event_pt row: target are the PT a PT activity's rewards need
// (ActivityPtData.targets), link_id the boss activity it belongs to
// (GetActBossLinkPTActID)
type eventPtConfig struct {
	Target []uint32 `json:"target"`
	LinkID uint32   `json:"link_id"`
}

var (
	permanentActivities map[uint32]permanentActivityConfig
	eventPtConfigs      map[uint32]eventPtConfig
)

func loadPermanentData() error {
	err := gamedata.LoadAll(
		gamedata.Load("activity_task_permanent", &permanentActivities),
		gamedata.Load("activity_event_pt", &eventPtConfigs),
	)
	if err != nil {
		return err
	}
	for id, config := range permanentActivities {
		config.eliminate = gamedata.IDs(config.EliminateTaskID)
		permanentActivities[id] = config
	}
	return nil
}

// permanentType is ActivityPermanentProxy:GetActivityTypeById, 0 when id
// isn't a permanent activity.
func permanentType(id uint32) uint32 {
	config, ok := permanentActivities[id]
	switch {
	case !ok:
		return 0
	case config.ActivityGroup >= 1 && config.ActivityGroup <= 1999:
		return permanentTypeNormal
	case config.ActivityGroup >= 2000 && config.ActivityGroup <= 2999:
		return permanentTypeRemaster
	}
	return 0
}

// commanderActivities are the activities the commander runs at their own
// time rather than at their activity_template one, with when each stops
// (0 never): the running permanent activities, and the activities of the
// commander's Capsule Rerun until they end, and the new commander
// activities (newUserActivities).
func commanderActivities(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (map[uint32]uint32, error) {
	own, err := newUserActivities(ctx, queries, commanderID, now)
	if err != nil {
		return nil, err
	}
	permanent, err := queries.ListPermanentActivities(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	for _, row := range permanent {
		if row.Running {
			own[row.ActivityID] = 0
		}
	}
	reruns, err := queries.ListRemasters(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	if len(reruns) > 0 {
		latest := reruns[len(reruns)-1]
		for id, stop := range rerunActivityStops(latest.RemasterID, latest.TimerID) {
			if stop.After(now) {
				own[id] = uint32(stop.Unix())
			}
		}
	}
	return own, nil
}

// permanentState is SC_11210: the permanent activities finished and the
// ones running.
func permanentState(ctx context.Context, queries *db.Queries, commanderID uint32) (*protobuf.SC_11210, error) {
	rows, err := queries.ListPermanentActivities(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	msg := &protobuf.SC_11210{}
	for _, row := range rows {
		if row.Finished {
			msg.PermanentActivity = append(msg.PermanentActivity, row.ActivityID)
		}
		if row.Running {
			msg.PermanentNow = append(msg.PermanentNow, row.ActivityID)
		}
	}
	return msg, nil
}

// handlePermanentStart answers ActivityPermanentStartCommand (CS_11206):
// the mini-event picked in the gallery, or the boss rush archive picked in
// the War Archives, starts from scratch and becomes one of the commander's
// activities (SC_11201 before the reply: the client opens its page as soon
// as SC_11207 comes, ActivityMainScene:verifyTabs). One runs at a time per
// type: the client stops the boss rush archive running first
// (ActivityPermanentStopCommand) and only offers a mini-event while none
// runs (activity_permanent_tips4). A finished mini-event can't be replayed.
func (game *Game) handlePermanentStart(client *connection.Client, req *protobuf.CS_11206) error {
	ctx := context.Background()
	id := req.GetActivityId()
	kind := permanentType(id)
	config, ok := activityConfigs[id]
	if kind == 0 || !ok {
		return client.Send(&protobuf.SC_11207{Result: result(errRefused)})
	}
	now := time.Now()
	err := game.tx(ctx, client, func(tx *Tx) error {
		rows, err := tx.ListPermanentActivities(ctx, client.CommanderID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.ActivityID == id && row.Finished {
				return errRefused
			}
			if row.Running && permanentType(row.ActivityID) == kind {
				return errRefused
			}
		}
		if err := tx.StartPermanentActivity(ctx, db.StartPermanentActivityParams{CommanderID: client.CommanderID, ActivityID: id}); err != nil {
			return err
		}
		// the commander manual's "open the Activity Archive" task counts it
		tx.event(TaskEvent{SubType: TaskSubPermanentActivity, Count: 1})
		state := newActivityStateAt(config, now)
		state.CommanderID = client.CommanderID
		if err := tx.SaveActivity(ctx, db.SaveActivityParams(state)); err != nil {
			return err
		}
		fleets, err := activityFleets(ctx, tx.Queries, client.CommanderID)
		if err != nil {
			return err
		}
		tx.push(&protobuf.SC_11201{ActivityInfo: activityInfo(config, state, 0, fleets[id])})
		return nil
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_11207{Result: result(err)})
}

// handlePermanentEnd answers ActivityPermanentFinishCommand (typ 1) and
// ActivityPermanentStopCommand (typ 2), CS_11208, on a running permanent
// activity. Finishing needs what Activity:canPermanentFinish checks: a task
// list's last day of tasks claimed, or a PT activity's rewards all claimed
// (ActivityPtData level). Either way the activity's eliminate_task_id tasks
// still unclaimed are taken back (SC_20004); the client drops the activity
// itself.
func (game *Game) handlePermanentEnd(client *connection.Client, req *protobuf.CS_11208) error {
	ctx := context.Background()
	id := req.GetActivityId()
	typ := req.GetTyp()
	config, ok := activityConfigs[id]
	if permanentType(id) == 0 || !ok || (typ != permanentFinish && typ != permanentStop) {
		return client.Send(&protobuf.SC_11209{Result: result(errRefused)})
	}
	err := game.tx(ctx, client, func(tx *Tx) error {
		key := db.StopPermanentActivityParams{CommanderID: client.CommanderID, ActivityID: id}
		var ended int64
		var err error
		if typ == permanentFinish {
			done, err := tx.permanentDone(config)
			if err != nil {
				return err
			}
			if !done {
				return errRefused
			}
			ended, err = tx.FinishPermanentActivity(ctx, db.FinishPermanentActivityParams(key))
			if err != nil {
				return err
			}
		} else if ended, err = tx.StopPermanentActivity(ctx, key); err != nil {
			return err
		}
		if ended == 0 {
			return errRefused // not running
		}
		return tx.dropUnclaimedTasks(permanentActivities[id].eliminate)
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_11209{Result: result(err)})
}

// permanentDone is Activity:canPermanentFinish on the stored state.
func (tx *Tx) permanentDone(config activityConfig) (bool, error) {
	switch config.Type {
	case activityTypeTaskList:
		var days [][]uint32
		if json.Unmarshal(config.ConfigData, &days) != nil || len(days) == 0 {
			return false, nil
		}
		for _, taskID := range days[len(days)-1] {
			task, err := tx.GetTask(tx.ctx, db.GetTaskParams{CommanderID: tx.commanderID, TaskID: taskID})
			if errors.Is(err, sql.ErrNoRows) || (err == nil && task.SubmitTime == 0) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
		}
		return true, nil
	case activityTypePTBuff:
		targets := eventPtConfigs[config.ConfigID].Target
		state, err := activityState(tx.ctx, tx.Queries, tx.commanderID, config)
		if err != nil {
			return false, err
		}
		claimed := slices.Sorted(slices.Values(state.Data1List))
		// ActivityPtData:Update: the level is how many targets, in order,
		// were claimed
		level := 0
		for level < len(claimed) && level < len(targets) && claimed[level] == targets[level] {
			level++
		}
		return len(targets) > 0 && level >= len(targets), nil
	}
	return false, nil
}

// dropUnclaimedTasks takes back the commander's tasks among ids that aren't
// claimed (SC_20004).
func (tx *Tx) dropUnclaimedTasks(ids []uint32) error {
	var dropped []uint32
	for _, id := range ids {
		task, err := tx.GetTask(tx.ctx, db.GetTaskParams{CommanderID: tx.commanderID, TaskID: id})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if task.SubmitTime == 0 {
			dropped = append(dropped, id)
		}
	}
	if len(dropped) == 0 {
		return nil
	}
	if err := tx.DeleteTasks(tx.ctx, db.DeleteTasksParams{CommanderID: tx.commanderID, TaskIds: dropped}); err != nil {
		return err
	}
	tx.push(&protobuf.SC_20004{IdList: dropped})
	return nil
}

// permanentClaimedTasks are the claimed tasks of the commander's running
// permanent task lists: TaskProxy keeps them as finished (SC_20001 with
// their submit_time) and canPermanentFinish reads the last day's from there.
func permanentClaimedTasks(ctx context.Context, queries *db.Queries, commanderID uint32) (map[uint32]bool, error) {
	rows, err := queries.ListPermanentActivities(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	tasks := map[uint32]bool{}
	for _, row := range rows {
		config, ok := activityConfigs[row.ActivityID]
		if !row.Running || !ok || config.Type != activityTypeTaskList {
			continue
		}
		for _, id := range config.tasks {
			tasks[id] = true
		}
	}
	return tasks, nil
}
