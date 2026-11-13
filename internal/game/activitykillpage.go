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
// one phase a day from the event's first day). data3 keeps the claimed
// phases, a bit each.
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
		if state.Data1 < pt.Target[index] {
			return errRefused
		}
		// a phase opens on its day of the event (1-based)
		if day := uint32(now.Sub(config.Time.Start).Hours()/24) + 1; index+1 > int(day) {
			return errRefused
		}
		if index < 32 && state.Data3&(1<<uint(index)) != 0 {
			return errRefused
		}
		drops := configDrops([][]uint32{pt.DropClient[index]})
		if err := tx.grantDrops(drops); err != nil {
			return err
		}
		if index < 32 {
			state.Data3 |= 1 << uint(index)
		}
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
