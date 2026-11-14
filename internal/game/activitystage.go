package game

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// activityPageTasks are the task ids an activity's page names: in its
// config_data (the story pages' [29068] or [29065, 29066, 29067], see
// taskActivities) and in its config_client (the stage page's task list, the
// story page's task_id).
func activityPageTasks(config activityConfig) []uint32 {
	var client struct {
		Task   []uint32 `json:"task"`
		TaskID uint32   `json:"task_id"`
	}
	var ids []uint32
	if json.Unmarshal(config.ConfigClient, &client) == nil {
		ids = append(ids, client.Task...)
		if client.TaskID != 0 {
			ids = append(ids, client.TaskID)
		}
	}
	return append(ids, config.tasks...)
}

// activityPageReply accepts the page's tasks and answers with them: the new
// ones are pushed (SC_20003) and every one of them comes back in task_list.
// The client leaves an event's page entries (a stage, a story) locked until
// their task is running, so this is what opens them.
func (game *Game) activityPageReply(client *connection.Client, ids []uint32) (*protobuf.SC_11203, error) {
	ctx := context.Background()
	var added *protobuf.SC_20003
	err := game.tx(ctx, client, func(tx *Tx) error {
		var err error
		added, err = acceptTasks(ctx, tx.Queries, client.CommanderID, ids, uint32(time.Now().Unix()))
		return err
	})
	if err != nil {
		return nil, err
	}
	if added != nil {
		if err := client.Push(added); err != nil {
			return nil, err
		}
	}
	return game.activityPageTaskList(ctx, client.CommanderID, ids)
}

// activityPageTaskList is SC_11203's task_list: the page's tasks the commander
// holds, with their progress.
func (game *Game) activityPageTaskList(ctx context.Context, commanderID uint32, ids []uint32) (*protobuf.SC_11203, error) {
	reply := &protobuf.SC_11203{Result: result(nil)}
	tasks, err := game.DB.ListTasks(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	wanted := map[uint32]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	for _, task := range tasks {
		if !wanted[task.TaskID] {
			continue
		}
		reply.TaskList = append(reply.TaskList, &protobuf.TASKINFO{
			Id:         proto.Uint32(task.TaskID),
			Progress:   proto.Uint32(task.Progress),
			AcceptTime: proto.Uint32(task.AcceptTime),
		})
	}
	return reply, nil
}

// activityStoryOperation answers an event's story page (activity type 13:
// 51301's stories, 51306/51307's task lists). The stories themselves are the
// client's (it reports them read with CS_11017, counted by the tasks' sub
// type 1005); the page only needs its tasks handed out and the op
// acknowledged.
func (game *Game) activityStoryOperation(client *connection.Client, config activityConfig, req *protobuf.CS_11202) error {
	if req.GetCmd() != 1 && req.GetCmd() != 2 {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	ids := activityPageTasks(config)
	if len(ids) == 0 {
		return client.Send(&protobuf.SC_11203{Result: result(nil)})
	}
	reply, err := game.activityPageReply(client, ids)
	if refused(err) {
		return client.Send(&protobuf.SC_11203{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}

// activityStageIDs are the stages a page's tasks count: their target ids
// (2079001-2079003 for the TwoPointFive page), the only ones a win can name.
func activityStageIDs(ids []uint32) map[uint32]bool {
	stages := map[uint32]bool{}
	for _, id := range ids {
		config, ok := taskConfigs[id]
		if !ok {
			continue
		}
		for _, stage := range config.target.ids {
			stages[stage] = true
		}
	}
	return stages
}

// activityStageClear answers an event's stage page (activity type 111,
// TwoPointFiveStagePage: activity 51304's stages 2079001-2079003). The client
// runs those fleet-less boss battles itself, so the page's tasks are what the
// server keeps: cmd 1 (the page opens) hands them out, cmd 2 (a stage's win,
// arg 1 its expedition id) counts the win for them (task_data_template sub
// type 20 with the stage as target) and appends the stage to data2_list, the
// cleared stages the page unlocks the next ones from (Activity.data2_list).
func (game *Game) activityStageClear(client *connection.Client, config activityConfig, req *protobuf.CS_11202) error {
	ids := activityPageTasks(config)
	if len(ids) == 0 || (req.GetCmd() != 1 && req.GetCmd() != 2) || (req.GetCmd() == 2 && req.GetArg1() == 0) {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	if req.GetCmd() == 2 {
		stage := req.GetArg1()
		if !activityStageIDs(ids)[stage] {
			return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
		}
		ctx := context.Background()
		var added *protobuf.SC_20003
		err := game.tx(ctx, client, func(tx *Tx) error {
			state, err := activityState(ctx, tx.Queries, client.CommanderID, config)
			if err != nil {
				return err
			}
			if !slices.Contains(state.Data2List, stage) {
				state.Data2List = append(state.Data2List, stage)
				if err := tx.Queries.SaveActivity(ctx, db.SaveActivityParams(state)); err != nil {
					return err
				}
			}
			// the page's tasks are handed out before the win is counted, so
			// the first clear counts for the task accepted with it
			added, err = acceptTasks(ctx, tx.Queries, client.CommanderID, ids, uint32(time.Now().Unix()))
			if err != nil {
				return err
			}
			tx.event(TaskEvent{SubType: TaskSubWin, Target: stage, Count: 1})
			return nil
		})
		if refused(err) {
			return client.Send(&protobuf.SC_11203{Result: result(err)})
		}
		if err != nil {
			return err
		}
		if added != nil {
			if err := client.Push(added); err != nil {
				return err
			}
		}
		reply, err := game.activityPageTaskList(ctx, client.CommanderID, ids)
		if refused(err) {
			return client.Send(&protobuf.SC_11203{Result: result(err)})
		}
		if err != nil {
			return err
		}
		return client.Send(reply)
	}
	reply, err := game.activityPageReply(client, ids)
	if refused(err) {
		return client.Send(&protobuf.SC_11203{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}
