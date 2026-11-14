package game

import (
	"context"
	"slices"
	"time"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// activityKillPageOperation answers an event's kill page (activity type 74,
// the TwoPointFive event's 51303): the commander's kills during the event are
// the activity's data1 (addEventKills), the phases' thresholds the
// activity_event_pt row's target list and each phase's reward its
// drop_client at the same index. cmd 1 with the phase's threshold in arg 1
// claims it once the kills reached it and the phase's day came (day_unlock:
// one phase a day from the event's first day, data2). data1_list keeps the
// claimed phases' thresholds, which is what the client's ActivityPtData reads
// them back from (ActivityNewPtOPCommand adds arg1 to it on success).
func (game *Game) activityKillPageOperation(client *connection.Client, config activityConfig, req *protobuf.CS_11202) error {
	pt, ok := eventPts[config.ConfigID]
	if !ok || len(pt.Target) == 0 || len(pt.DropClient) != len(pt.Target) {
		// not a kill page: acknowledge, nothing to pay
		return client.Send(&protobuf.SC_11203{Result: result(nil)})
	}
	if req.GetCmd() != 1 {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	index := slices.Index(pt.Target, req.GetArg1())
	if index < 0 {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	ctx := context.Background()
	now := time.Now()
	reply := &protobuf.SC_11203{Result: result(nil)}
	err := game.tx(ctx, client, func(tx *Tx) error {
		state, err := activityState(ctx, tx.Queries, client.CommanderID, config)
		if err != nil {
			return err
		}
		if state.Data2 == 0 {
			// the client counts the phases' days from data2 (ActivityPtData's
			// startTime); a state stored before this page kept its start would
			// otherwise look like every phase unlocked
			state.Data2 = uint32(config.Time.Start.Unix())
		}
		if state.Data1 < pt.Target[index] {
			return errRefused
		}
		// the phases are claimed in order: the client only offers the next
		// one (ActivityPtData:GetAward reads dropList[level])
		if index != len(state.Data1List) {
			return errRefused
		}
		// a phase opens on its day of the event (1-based), unless the row
		// doesn't say (day_unlock all 0)
		if index < len(pt.DayUnlock) {
			start := time.Unix(int64(state.Data2), 0)
			days := int(startOfDay(now).Sub(startOfDay(start)).Hours() / 24)
			if days < 0 {
				days = 0
			}
			if pt.DayUnlock[index] > uint32(days)+1 {
				return errRefused
			}
		}
		drops := configDrops([][]uint32{pt.DropClient[index]})
		if err := tx.grantDrops(drops); err != nil {
			return err
		}
		state.Data1List = append(state.Data1List, pt.Target[index])
		if err := tx.Queries.SaveActivity(ctx, db.SaveActivityParams(state)); err != nil {
			return err
		}
		reply.AwardList = drops
		return nil
	})
	if refused(err) {
		return client.Send(&protobuf.SC_11203{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}
