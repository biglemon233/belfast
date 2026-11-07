package game

import (
	"context"
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// The World Cruise, the game's battle pass (ActivityConst.PT_CRUSING, 54):
// battlepass_event_pt holds a pass's levels (the pt each one needs, its
// battlepass_event_award ids on the regular and the Fair Wind track), the
// weekly missions come from battlepass_task_group (activityTaskIDs) and the
// points themselves are the 59599 virtual item, kept as the
// TaskSubCruisePt counter (drops.go). belfast sells nothing, so the Fair
// Wind track is always open and a claim hands out both tracks at once and
// records the level once, in data1_list (the Fair Wind list the page reads,
// data2_list, is that same list).

// cruisePass is a battlepass_event_pt row.
type cruisePass struct {
	Pt       uint32   `json:"pt"` // the points' virtual item (59599)
	Target   []uint32 `json:"target"`
	Award    []uint32 `json:"award"`     // battlepass_event_award ids, regular
	AwardPay []uint32 `json:"award_pay"` // the same, Fair Wind
}

// cruiseAward is a battlepass_event_award row.
type cruiseAward struct {
	Drop []uint32 `json:"drop_client"`
}

var (
	cruisePasses map[uint32]cruisePass
	cruiseAwards map[uint32]cruiseAward
)

func loadCruiseData() error {
	return gamedata.LoadAll(
		gamedata.Load("battlepass_event_pt", &cruisePasses),
		gamedata.Load("battlepass_event_award", &cruiseAwards),
	)
}

// cruiseInfo fills the fields the pass page reads: data1 the points earned
// (the counter, or the stored value when it is higher, as the back-office
// sets it), data2 the Fair Wind track bought (always, belfast sells
// nothing) and data2_list its claims (the same as the regular track's).
func cruiseInfo(info *protobuf.ACTIVITYINFO, state db.CommanderActivity, pt uint32) {
	info.Data1 = proto.Uint32(max(pt, state.Data1))
	info.Data2 = proto.Uint32(1)
	info.Data2List = state.Data1List
}

// cruiseAwardDrops is what a battlepass_event_award gives.
func cruiseAwardDrops(id uint32) []*protobuf.DROPINFO {
	award, ok := cruiseAwards[id]
	if !ok || len(award.Drop) < 3 || award.Drop[2] == 0 {
		return nil
	}
	return []*protobuf.DROPINFO{dropInfo(award.Drop[0], award.Drop[1], award.Drop[2])}
}

// cruiseTarget reads what a claim names: a level's target pt, or a level
// number (1-based, as the client's buttons may count them).
func cruiseTarget(pass cruisePass, value uint32) (uint32, bool) {
	if slices.Contains(pass.Target, value) {
		return value, true
	}
	if value >= 1 && int(value) <= len(pass.Target) {
		return pass.Target[value-1], true
	}
	return 0, false
}

// cruiseOperation claims cruise pass levels (ActivityOperationCommand,
// CS_11202, cmd 1): the levels the request names (arg1..3, arg_list) or,
// naming none, every level the points reached. A level gives its regular
// and its Fair Wind award and is recorded once; a level already claimed, or
// one above the points, is left out. Nothing to claim refuses.
func (game *Game) cruiseOperation(client *connection.Client, config activityConfig, req *protobuf.CS_11202) error {
	pass, ok := cruisePasses[config.ID]
	if !ok || len(pass.Target) == 0 {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	logger.WithFields("Cruise",
		logger.FieldValue("activity", config.ID),
		logger.FieldValue("cmd", req.GetCmd()),
		logger.FieldValue("arg1", req.GetArg1()),
		logger.FieldValue("arg2", req.GetArg2()),
		logger.FieldValue("args", len(req.GetArgList())),
	).Info("claim")
	reply := &protobuf.SC_11203{Result: result(nil)}
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		state, err := activityState(tx.ctx, tx.Queries, tx.commanderID, config)
		if err != nil {
			return err
		}
		pt, err := taskCounter(tx.ctx, tx.Queries, tx.commanderID, TaskSubCruisePt, 0)
		if err != nil {
			return err
		}
		pt = max(pt, state.Data1)
		var targets []uint32
		named := append([]uint32{req.GetArg1(), req.GetArg2(), req.GetArg3()}, req.GetArgList()...)
		for _, value := range named {
			if value == 0 {
				continue
			}
			if target, resolved := cruiseTarget(pass, value); resolved && !slices.Contains(targets, target) {
				targets = append(targets, target)
			}
		}
		if len(targets) == 0 {
			targets = slices.Clone(pass.Target)
		}
		slices.Sort(targets)
		var awards []*protobuf.DROPINFO
		for _, target := range targets {
			i := slices.Index(pass.Target, target)
			if i < 0 || target > pt || slices.Contains(state.Data1List, target) {
				continue
			}
			if i < len(pass.Award) {
				awards = append(awards, cruiseAwardDrops(pass.Award[i])...)
			}
			if i < len(pass.AwardPay) {
				awards = append(awards, cruiseAwardDrops(pass.AwardPay[i])...)
			}
			state.Data1List = append(state.Data1List, target)
		}
		if len(awards) == 0 {
			return errRefused
		}
		state.Data1 = pt
		slices.Sort(state.Data1List)
		if err := tx.SaveActivity(tx.ctx, db.SaveActivityParams(state)); err != nil {
			return err
		}
		reply.AwardList = awards
		return tx.grantDrops(awards)
	})
	if refused(err) {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}
