package game

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// meowfficerInfo is a COMMANDERINFO, read into a Commander
// (model/vo/commander/commander.lua). Every cat has its skill: the detail
// and new cat pages read getSkills()[1].
func meowfficerInfo(cat db.Meowfficer) *protobuf.COMMANDERINFO {
	locked := uint32(0)
	if cat.Locked {
		locked = 1
	}
	return &protobuf.COMMANDERINFO{
		Id:            proto.Uint32(cat.ID),
		TemplateId:    proto.Uint32(cat.TemplateID),
		Level:         proto.Uint32(cat.Level),
		Exp:           proto.Uint32(cat.Exp),
		IsLocked:      proto.Uint32(locked),
		Ability:       cat.Talents,
		AbilityOrigin: cat.TalentOrigins,
		AbilityTime:   proto.Uint32(uint32(cat.TalentTime)),
		Skill:         []*protobuf.SKILLINFO{{Id: proto.Uint32(cat.SkillID), Exp: proto.Uint32(cat.SkillExp)}},
		UsedPt:        proto.Uint32(cat.UsedPt),
		Name:          proto.String(cat.Name),
		RenameTime:    proto.Uint32(uint32(cat.RenameTime)),
		HomeCleanTime: proto.Uint32(uint32(cat.HomeCleanTime)),
		HomePlayTime:  proto.Uint32(uint32(cat.HomePlayTime)),
		HomeFeedTime:  proto.Uint32(uint32(cat.HomeFeedTime)),
	}
}

// meowfficerBoxInfo is a COMMANDERBOXINFO, read into a CommanderBox: no
// finish_time is an empty box, a begin_time to come a queued one.
func meowfficerBoxInfo(box db.MeowfficerBox) *protobuf.COMMANDERBOXINFO {
	return &protobuf.COMMANDERBOXINFO{
		Id:         proto.Uint32(box.BoxID),
		PoolId:     proto.Uint32(box.PoolID),
		FinishTime: proto.Uint32(uint32(box.FinishTime)),
		BeginTime:  proto.Uint32(uint32(box.BeginTime)),
	}
}

// meowfficerState is the commander's meowfficer_states row, the defaults
// when there is none.
func meowfficerState(ctx context.Context, queries *db.Queries, commanderID uint32) (db.MeowfficerState, error) {
	state, err := queries.GetMeowfficerState(ctx, commanderID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.MeowfficerState{CommanderID: commanderID, HomeLevel: 1}, nil
	}
	return state, err
}

// boxesBoughtToday is SC_25001 usage_count: the cat boxes bought today.
func boxesBoughtToday(state db.MeowfficerState, now time.Time) uint32 {
	if !sameServerDay(time.Unix(state.ReserveDay, 0), now) {
		return 0
	}
	return state.ReserveCount
}

// meowfficerList is SC_25001, CommanderProxy's login state: the cats, the
// boxes that aren't empty (the client makes the commander_box_count boxes
// itself), the cat boxes bought today and the saved pairs. A new account's
// is only usage_count 0 (captured).
func meowfficerList(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (*protobuf.SC_25001, error) {
	cats, err := queries.ListMeowfficers(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	boxes, err := queries.ListMeowfficerBoxes(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	state, err := meowfficerState(ctx, queries, commanderID)
	if err != nil {
		return nil, err
	}
	presets, err := queries.ListMeowfficerPresets(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	slots, err := queries.ListMeowfficerPresetSlots(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	msg := &protobuf.SC_25001{UsageCount: proto.Uint32(boxesBoughtToday(state, now))}
	for _, cat := range cats {
		msg.Commanders = append(msg.Commanders, meowfficerInfo(cat))
	}
	for _, box := range boxes {
		msg.Box = append(msg.Box, meowfficerBoxInfo(box))
	}
	for _, preset := range presets {
		fleet := &protobuf.PRESETFLEET{Id: proto.Uint32(preset.PresetID), Name: proto.String(preset.Name)}
		for _, slot := range slots {
			if slot.PresetID == preset.PresetID {
				fleet.Commandersid = append(fleet.Commandersid, &protobuf.COMMANDERSINFO{Pos: proto.Uint32(slot.Pos), Id: proto.Uint32(slot.MeowfficerID)})
			}
		}
		msg.Presets = append(msg.Presets, fleet)
	}
	return msg, nil
}

// boxQueue orders boxes as they train: by begin time, then id.
func boxQueue(boxes []db.MeowfficerBox) []db.MeowfficerBox {
	queue := slices.Clone(boxes)
	slices.SortStableFunc(queue, func(a, b db.MeowfficerBox) int {
		if a.BeginTime != b.BeginTime {
			return int(a.BeginTime - b.BeginTime)
		}
		return int(a.BoxID) - int(b.BoxID)
	})
	return queue
}

// boxStart is when a box queued behind placed ones starts training: when
// one of the meowfficerBoxWorkers frees up, now at the earliest (as builds,
// buildStart).
func boxStart(placed []db.MeowfficerBox, now int64) int64 {
	finishes := make([]int64, 0, len(placed))
	for _, box := range placed {
		finishes = append(finishes, box.FinishTime)
	}
	slices.Sort(finishes)
	slices.Reverse(finishes)
	if len(finishes) < meowfficerBoxWorkers {
		return now
	}
	return max(now, finishes[meowfficerBoxWorkers-1])
}

// rescheduleBoxes saves boxes (in queue order) after some got shorter: the
// ones still waiting move up behind the others, keeping their training time.
func rescheduleBoxes(tx *Tx, queue []db.MeowfficerBox, now int64) error {
	var placed []db.MeowfficerBox
	for _, box := range queue {
		if box.BeginTime > now {
			duration := box.FinishTime - box.BeginTime
			box.BeginTime = boxStart(placed, now)
			box.FinishTime = box.BeginTime + duration
		}
		placed = append(placed, box)
		if err := tx.SetMeowfficerBoxTimes(tx.ctx, db.SetMeowfficerBoxTimesParams{
			BeginTime: box.BeginTime, FinishTime: box.FinishTime, CommanderID: tx.commanderID, BoxID: box.BoxID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// handleTrainMeowfficerBox starts training a cat box (BuildCommaderCommand,
// CS_25002, from the boxes page's start button and NG007): boxid is the
// pool (commander_data_create_material) picked, the box goes in the first
// empty one of the commander_box_count, behind the ones already training.
// What it holds is drawn now: a regular cat of the pool's rarity, which
// trains in its own time (meowfficerTimes). On result 0 the client takes
// the box item off its bag itself and shows the box SC_25003 returns. No
// official capture has it: built from the Lua alone.
func (game *Game) handleTrainMeowfficerBox(client *connection.Client, req *protobuf.CS_25002) error {
	now := time.Now().Unix()
	pool, ok := meowfficerPools[req.GetBoxid()]
	var trained db.MeowfficerBox
	err := errRefused
	if ok && len(meowfficerCandidates[pool.ID]) > 0 {
		err = game.tx(context.Background(), client, func(tx *Tx) error {
			boxes, err := tx.ListMeowfficerBoxes(tx.ctx, tx.commanderID)
			if err != nil {
				return err
			}
			id := uint32(1)
			for _, box := range boxes {
				if box.BoxID == id {
					id++
				}
			}
			if id > meowfficerBoxCount {
				return errRefused
			}
			if err := tx.SpendItem(pool.UseItem, pool.Number); err != nil {
				return err
			}
			candidates := meowfficerCandidates[pool.ID]
			template := candidates[rand.IntN(len(candidates))]
			begin := boxStart(boxes, now)
			trained = db.MeowfficerBox{
				CommanderID: tx.commanderID, BoxID: id, PoolID: pool.ID, TemplateID: template,
				BeginTime: begin, FinishTime: begin + int64(meowfficerTimes[template]),
			}
			return tx.CreateMeowfficerBox(tx.ctx, db.CreateMeowfficerBoxParams{
				CommanderID: tx.commanderID, BoxID: id, PoolID: pool.ID, TemplateID: template,
				BeginTime: trained.BeginTime, FinishTime: trained.FinishTime,
			})
		})
	}
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_25003{Result: result(err), Box: meowfficerBoxInfo(trained)})
}

// handleOpenMeowfficerBox takes the cat out of a finished box
// (GetCommanderCommand, CS_25004, NG008): the client checks the box is
// finished and commander_bag_max has room, adds SC_25005's commander and
// empties the box. The cat comes with its starting talents
// (meowfficerStartingTalents). finish_time is never read; it is the box's.
// Built from the Lua alone.
func (game *Game) handleOpenMeowfficerBox(client *connection.Client, req *protobuf.CS_25004) error {
	now := time.Now().Unix()
	var cat db.Meowfficer
	var finished int64
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		boxes, err := tx.ListMeowfficerBoxes(tx.ctx, tx.commanderID)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(boxes, func(box db.MeowfficerBox) bool { return box.BoxID == req.GetBoxid() })
		if i < 0 || boxes[i].FinishTime > now {
			return errRefused
		}
		box := boxes[i]
		template, ok := meowfficerTemplates[box.TemplateID]
		if !ok {
			return errRefused
		}
		commander, err := tx.GetCommander(tx.ctx, tx.commanderID)
		if err != nil {
			return err
		}
		owned, err := tx.CountMeowfficers(tx.ctx, tx.commanderID)
		if err != nil {
			return err
		}
		if owned >= int64(commander.CommanderBagMax) {
			return errRefused
		}
		if err := tx.DeleteMeowfficerBox(tx.ctx, db.DeleteMeowfficerBoxParams{CommanderID: tx.commanderID, BoxID: box.BoxID}); err != nil {
			return err
		}
		finished = box.FinishTime
		cat, err = tx.createMeowfficer(template)
		return err
	})
	if err != nil && !refused(err) {
		return err
	}
	reply := &protobuf.SC_25005{Result: result(err), FinishTime: proto.Uint32(uint32(finished))}
	if err == nil {
		reply.Commander = meowfficerInfo(cat)
	}
	return client.Send(reply)
}

// createMeowfficer gives the commander a new cat of template, with its
// starting talents (meowfficerStartingTalents).
func (tx *Tx) createMeowfficer(template meowfficerTemplate) (db.Meowfficer, error) {
	talents := meowfficerStartingTalents(template.ID, rand.IntN)
	return tx.CreateMeowfficer(tx.ctx, db.CreateMeowfficerParams{
		CommanderID: tx.commanderID, TemplateID: template.ID, SkillID: template.SkillID,
		Talents: talents, TalentOrigins: talents,
	})
}

// grantMeowfficers gives a reward's cats (DROP_TYPE_COMMANDER_CAT: the
// client's Drop.AddDropCase does nothing for them, CommanderProxy adds the
// SC_25039 grantDrops pushes). A special cat the commander has already
// turns into its meowfficerDuplicates resource instead: entry is rewritten
// so the reply lists the coins, which the client adds, and no cat (its
// award window would show the newest cat again, and nothing would tell it
// of the gold). Copies of a special beyond the first (no config gives
// more than one) are turned into the resource too, unlisted. No
// reward path of the client checks the cattery's room (only opening a box
// does, GetCommanderCommand's commander_capcity_is_max; the mail claim's
// CheckOverflow covers gold, oil, gear and ships), and its checks all read
// commander_bag_max <= count: a reward is never refused and may house more
// cats than the cattery holds.
func (tx *Tx) grantMeowfficers(entry *protobuf.DROPINFO) ([]*protobuf.COMMANDERINFO, error) {
	template, ok := meowfficerTemplates[entry.GetId()]
	if !ok {
		logger.WithFields("Drops", logger.FieldValue("meowfficer", entry.GetId())).Warn("drop not stored")
		return nil, nil
	}
	count := entry.GetNumber()
	if restore, unique := meowfficerDuplicates[template.ID]; unique && count > 0 {
		owned, err := tx.ownedCount(dropTypeMeowfficer, template.ID)
		if err != nil {
			return nil, err
		}
		copies := count - 1
		if owned > 0 {
			copies = count
		}
		if copies > 0 {
			if err := tx.AddResource(restore.GetId(), restore.GetNumber()*copies); err != nil {
				return nil, err
			}
		}
		if owned > 0 {
			entry.Type, entry.Id, entry.Number = proto.Uint32(restore.GetType()), proto.Uint32(restore.GetId()), proto.Uint32(restore.GetNumber()*copies)
			return nil, nil
		}
		count = 1
		entry.Number = proto.Uint32(1)
	}
	var cats []*protobuf.COMMANDERINFO
	for range count {
		cat, err := tx.createMeowfficer(template)
		if err != nil {
			return nil, err
		}
		cats = append(cats, meowfficerInfo(cat))
	}
	return cats, nil
}

// handleRefreshMeowfficerBoxes answers RefreshCommanderBoxesCommand
// (CS_25034, after a quick finish and when the boxes page opens): every box
// in id order, the empty ones too so the client drops what it had for
// them. Built from the Lua alone.
func (game *Game) handleRefreshMeowfficerBoxes(client *connection.Client, req *protobuf.CS_25034) error {
	boxes, err := game.DB.ListMeowfficerBoxes(context.Background(), client.CommanderID)
	if err != nil {
		return err
	}
	reply := &protobuf.SC_25035{}
	for id := uint32(1); id <= meowfficerBoxCount; id++ {
		box := db.MeowfficerBox{BoxID: id}
		if i := slices.IndexFunc(boxes, func(box db.MeowfficerBox) bool { return box.BoxID == id }); i >= 0 {
			box = boxes[i]
		}
		reply.BoxList = append(reply.BoxList, meowfficerBoxInfo(box))
	}
	return client.Send(reply)
}

// handleQuickFinishMeowfficerBoxes spends Cat Box Quick Finishers on the
// boxes training or queued (CommanderQuicklyFinishBoxesCommand, CS_25037):
// each takes its usage_arg seconds off, spent on the boxes in training
// order, as many as a box still needs (CommanderProxy:CalcQuickItemUsageCnt);
// the queued ones then move up. finish_cnt and affect_cnt are the client's
// own count. On result 0 the client removes item_cnt finishers and asks for
// the boxes again (CS_25034). Built from the Lua alone.
func (game *Game) handleQuickFinishMeowfficerBoxes(client *connection.Client, req *protobuf.CS_25037) error {
	now := time.Now().Unix()
	err := errRefused
	if req.GetItemCnt() > 0 {
		err = game.tx(context.Background(), client, func(tx *Tx) error {
			boxes, err := tx.ListMeowfficerBoxes(tx.ctx, tx.commanderID)
			if err != nil {
				return err
			}
			if err := tx.SpendItem(itemCatBoxQuickFinisher, req.GetItemCnt()); err != nil {
				return err
			}
			left := req.GetItemCnt()
			step := int64(catBoxQuickFinisherSeconds)
			queue := boxQueue(boxes)
			for i, box := range queue {
				if box.FinishTime <= now || left == 0 {
					continue
				}
				// a box training is shortened from now, a queued one by its
				// whole training time (it moves up with rescheduleBoxes)
				remaining := box.FinishTime - max(box.BeginTime, now)
				used := min(uint32((remaining+step-1)/step), left)
				left -= used
				queue[i].FinishTime = max(box.BeginTime, box.FinishTime-int64(used)*step)
				if box.BeginTime > now && queue[i].FinishTime == box.BeginTime {
					// a queued box finished outright is done now
					queue[i].BeginTime, queue[i].FinishTime = now, now
				}
			}
			if left == req.GetItemCnt() {
				return errRefused
			}
			return rescheduleBoxes(tx, queue, now)
		})
	}
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_25038{Result: result(err)})
}

// quickFinishMeowfficerBox spends cat box quick finishers on one box, the
// per-box "accelerate" button (CS_15002 on item 20010,
// CommanderBoxes:useItem): arg names the box, or the one training when it
// names none. count of the item takes count x its usage_arg seconds off
// (CommanderProxy:CalcQuickItemUsageCnt), never more than the box still
// needs, and the boxes queued behind it move up (rescheduleBoxes). A box
// with nothing left to shorten refuses, so the items stay.
func (tx *Tx) quickFinishMeowfficerBox(arg []uint32, count uint32) error {
	now := time.Now().Unix()
	boxes, err := tx.ListMeowfficerBoxes(tx.ctx, tx.commanderID)
	if err != nil {
		return err
	}
	queue := boxQueue(boxes)
	var i int
	if len(arg) > 0 {
		i = slices.IndexFunc(queue, func(box db.MeowfficerBox) bool { return box.BoxID == arg[0] })
	} else {
		i = slices.IndexFunc(queue, func(box db.MeowfficerBox) bool { return box.FinishTime > now })
	}
	if i < 0 {
		return errRefused
	}
	box := queue[i]
	step := int64(catBoxQuickFinisherSeconds)
	remaining := box.FinishTime - max(box.BeginTime, now)
	if remaining <= 0 {
		return errRefused
	}
	used := min(uint32((remaining+step-1)/step), count)
	if used == 0 {
		return errRefused
	}
	if err := tx.SpendItem(itemCatBoxQuickFinisher, used); err != nil {
		return err
	}
	queue[i].FinishTime = max(box.BeginTime, box.FinishTime-int64(used)*step)
	if box.BeginTime > now && queue[i].FinishTime == box.BeginTime {
		// a queued box finished outright is done now
		queue[i].BeginTime, queue[i].FinishTime = now, now
	}
	logger.WithFields("Meowfficer", logger.FieldValue("box", box.BoxID), logger.FieldValue("used", used)).Info("quick finish")
	return rescheduleBoxes(tx, queue, now)
}

// handleBuyMeowfficerBoxes buys cat boxes from Akashi
// (ReserveCommanderBoxCommand, CS_25018, NG006): type is how many. The nth
// box of the day costs commander_get_cost's gold (the first is free) less
// the guild technologies' discount (CommanderConst.getBoxComsume), at
// most MAX_GETBOX_CNT a day; each is a box item drawn with the rates the
// page shows (commander_build_rate_tip). The client takes the gold off
// itself, counts the boxes bought and adds the awards. The very first box
// a commander buys is an Elite one: the guide goes on to train an Elite box
// (NG007 picks pool 2 and is skipped without item 20012); no capture shows
// the official draw. Built from the Lua alone.
func (game *Game) handleBuyMeowfficerBoxes(client *connection.Client, req *protobuf.CS_25018) error {
	now := time.Now()
	count := req.GetType()
	var awards []*protobuf.DROPINFO
	err := errRefused
	if count > 0 {
		err = game.tx(context.Background(), client, func(tx *Tx) error {
			state, err := meowfficerState(tx.ctx, tx.Queries, tx.commanderID)
			if err != nil {
				return err
			}
			bought := boxesBoughtToday(state, now)
			if bought+count > meowfficerBoxDailyMax() {
				return errRefused
			}
			discount, err := tx.guildTechBonus(guildTechCatBoxGold)
			if err != nil {
				return err
			}
			gold := uint32(0)
			for n := bought; n < bought+count; n++ {
				gold += meowfficerBoxCost(n) - min(discount, meowfficerBoxCost(n))
			}
			if err := tx.SpendResource(ResourceGold, gold); err != nil {
				return err
			}
			for n := range count {
				item := meowfficerBoxRates[weightedPick(rand.Uint32N, meowfficerBoxRates, func(rate meowfficerBoxRate) uint32 { return rate.weight })].item
				if state.ReserveTotal == 0 && n == 0 {
					item = meowfficerPools[2].UseItem
				}
				awards = mergeDrop(awards, dropTypeItem, item, 1)
			}
			if err := tx.grantDrops(awards); err != nil {
				return err
			}
			if err := tx.CreateMeowfficerState(tx.ctx, tx.commanderID); err != nil {
				return err
			}
			return tx.SetMeowfficerReserve(tx.ctx, db.SetMeowfficerReserveParams{
				ReserveCount: bought + count, ReserveDay: now.Unix(), ReserveTotal: state.ReserveTotal + count, CommanderID: tx.commanderID,
			})
		})
	}
	if err != nil && !refused(err) {
		return err
	}
	if err != nil {
		awards = nil
	}
	return client.Send(&protobuf.SC_25019{Result: result(err), Awards: awards})
}

// handleEquipMeowfficer puts a cat in a fleet's command (pos 1) or support
// (pos 2) slot, or empties it with commanderid 0
// (EquipCommanderToFleetCommande, CS_25006). The client first takes the cat
// out of any other fleet or slot (SelectFleetCommanderCommand) and keeps two
// cats of the same group apart; both are refused here too. Built from the
// Lua alone.
func (game *Game) handleEquipMeowfficer(client *connection.Client, req *protobuf.CS_25006) error {
	fleetID, pos, catID := req.GetGroupid(), req.GetPos(), req.GetCommanderid()
	err := errRefused
	if pos >= 1 && pos <= meowfficerFleetSlots {
		err = game.tx(context.Background(), client, func(tx *Tx) error {
			fleets, err := tx.ListFleets(tx.ctx, tx.commanderID)
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(fleets, func(fleet db.ListFleetsRow) bool { return fleet.FleetID == fleetID }) {
				return errRefused
			}
			if catID == 0 {
				return tx.ClearFleetMeowfficer(tx.ctx, db.ClearFleetMeowfficerParams{CommanderID: tx.commanderID, FleetID: fleetID, Pos: pos})
			}
			cat, err := tx.GetMeowfficer(tx.ctx, db.GetMeowfficerParams{CommanderID: tx.commanderID, ID: catID})
			if errors.Is(err, sql.ErrNoRows) {
				return errRefused
			}
			if err != nil {
				return err
			}
			assigned, err := tx.ListFleetMeowfficers(tx.ctx, tx.commanderID)
			if err != nil {
				return err
			}
			for _, slot := range assigned {
				if slot.FleetID == fleetID && slot.Pos == pos {
					continue
				}
				if slot.MeowfficerID == catID {
					return errRefused
				}
				if slot.FleetID == fleetID {
					other, err := tx.GetMeowfficer(tx.ctx, db.GetMeowfficerParams{CommanderID: tx.commanderID, ID: slot.MeowfficerID})
					if err != nil {
						return err
					}
					if meowfficerTemplates[other.TemplateID].GroupType == meowfficerTemplates[cat.TemplateID].GroupType {
						return errRefused
					}
				}
			}
			return tx.SetFleetMeowfficer(tx.ctx, db.SetFleetMeowfficerParams{CommanderID: tx.commanderID, FleetID: fleetID, Pos: pos, MeowfficerID: catID})
		})
	}
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_25007{Result: result(err)})
}
