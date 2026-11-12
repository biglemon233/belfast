package game

import (
	"context"
	"encoding/json"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// The Commander Manual's research tasks (科研任务): activity 632, type 71.
// The activity holds a stage's catch-up task set ("一期" ... "七期"), each
// a phase of 20 tasks (19 in its ids and one more, the ship the stage gives);
// only one phase runs at a time. The manual's "开启任务" asks for one with cmd
// 1 and the phase, 1 based; the page then claims the finished tasks with cmd
// 2 (the catch-up tasks are things the commander already did, so a whole
// phase can be finished the moment it starts). Built from the tables.
const activityTypeResearchTask = 71

// researchTaskPhases reads a research task activity's phases: config_data is
// [group, id, [[task ids..., last], ...]].
func researchTaskPhases(config activityConfig) [][]uint32 {
	var data []json.RawMessage
	if json.Unmarshal(config.ConfigData, &data) != nil || len(data) < 3 {
		return nil
	}
	var phases []json.RawMessage
	if json.Unmarshal(data[2], &phases) != nil {
		return nil
	}
	var out [][]uint32
	for _, raw := range phases {
		var entry []json.RawMessage
		if json.Unmarshal(raw, &entry) != nil || len(entry) < 2 {
			continue
		}
		var ids []uint32
		if json.Unmarshal(entry[0], &ids) != nil {
			continue
		}
		if last := rawUint(entry[1]); last != 0 {
			ids = append(ids, last)
		}
		out = append(out, ids)
	}
	return out
}

// researchTaskOperation answers the manual's research task page. cmd 1 starts
// one of those phases (arg 1 the phase, 1 based), cmd 2 claims the phase's
// finished tasks; both answer with the running phase's tasks in task_list.
// Starting a second phase while one runs is refused (the manual's switch
// waits for the running one to finish).
func (game *Game) researchTaskOperation(client *connection.Client, config activityConfig, req *protobuf.CS_11202) error {
	ctx := context.Background()
	phases := researchTaskPhases(config)
	now := time.Now()
	var state db.CommanderActivity
	err := game.tx(ctx, client, func(tx *Tx) error {
		var err error
		state, err = activityState(ctx, tx.Queries, client.CommanderID, config)
		if err != nil {
			return err
		}
		switch req.GetCmd() {
		case 1:
			phase := int(req.GetArg1()) - 1
			if phase < 0 || phase >= len(phases) {
				return errRefused
			}
			if state.Data1 != 0 && int(state.Data1)-1 != phase {
				return errRefused
			}
			if state.Data1 == 0 {
				state.Data1 = uint32(phase + 1)
				state.Data2 = uint32(now.Unix())
				if err := tx.Queries.SaveActivity(ctx, db.SaveActivityParams(state)); err != nil {
					return err
				}
				if _, err := acceptTasks(ctx, tx.Queries, client.CommanderID, phases[phase], uint32(now.Unix())); err != nil {
					return err
				}
			}
		case 2:
			if state.Data1 == 0 {
				return errRefused
			}
		default:
			return errRefused
		}
		// the tasks the client counts itself ("ship at 115", "clear 8-4")
		// are the server's too, before anything is claimed
		return tx.syncStateTasks()
	})
	if refused(err) {
		return client.Send(&protobuf.SC_11203{Result: result(err)})
	}
	if err != nil {
		return err
	}
	phase := int(state.Data1) - 1
	if phase < 0 || phase >= len(phases) {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	reply := &protobuf.SC_11203{Result: result(nil)}
	if req.GetCmd() == 2 {
		var claims []taskClaimed
		for _, id := range phases[phase] {
			claim, ok, err := game.taskClaim(ctx, client.CommanderID, id)
			if err != nil {
				return err
			}
			if ok {
				claims = append(claims, claim)
			}
		}
		if len(claims) > 0 {
			awards, err := game.claimTasks(ctx, client, claims, nil)
			if err != nil && !refused(err) {
				return err
			}
			reply.AwardList = awards
		}
	}
	tasks, err := game.DB.ListTasks(ctx, client.CommanderID)
	if err != nil {
		return err
	}
	ids := map[uint32]bool{}
	for _, id := range phases[phase] {
		ids[id] = true
	}
	for _, task := range tasks {
		if !ids[task.TaskID] {
			continue
		}
		reply.TaskList = append(reply.TaskList, &protobuf.TASKINFO{
			Id:         proto.Uint32(task.TaskID),
			Progress:   proto.Uint32(task.Progress),
			AcceptTime: proto.Uint32(task.AcceptTime),
		})
	}
	return client.Send(reply)
}
