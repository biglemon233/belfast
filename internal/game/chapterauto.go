package game

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// Operational Handover (ChapterAutoProxy, controller/command/chapterauto):
// a stage cleared once on uninterrupted Auto-Repeat records how long that
// took, and can then be "run" a number of times in the background, each
// sortie taking that time out of a daily allowance and giving the Lecture
// Hall proficiency. A Handover Permit on a sortie costs the stage's oil and
// gives a stage clear's rewards as well. Nothing was captured: everything
// here is built from the Lua and the tables, the rewards of a permit sortie
// on belfast's own drops (scenarioDrops). Operation Siren's handover
// (CS_13018, ChapterAutoProxy.TYPE.WORLD) shares the sortie list and the
// claim: worldauto.go.

// ChapterAutoProxy.TYPE
const chapterAutoCampaign = 1

// ChapterAutoTicket.TYPE: what a Handover Permit is for
const (
	chapterTicketMain  = 1 // campaign sorties, or an hour more (CS_13016 ticket_num_1)
	chapterTicketWorld = 2 // Operation Siren
	chapterTicketTime  = 3 // an hour more (CS_13016 ticket_num_3)
)

// chapterTicketForever is ChapterAutoTicket.FOREVER_TIME, a permit that
// doesn't expire.
const chapterTicketForever = math.MaxUint32

// chapterAutoExpBook is ChapterAutoCommission.EXP_BOOK_ID: a permit sortie
// gives drop_expbook of it.
const chapterAutoExpBook = 16501

// chapterAutoConfig is a chapter_auto_statistics row, a stage the handover
// can run.
type chapterAutoConfig struct {
	ClassExp       uint32          `json:"base_class_exp"`     // proficiency a sortie gives
	Boss           json.RawMessage `json:"boss_expedition_id"` // the boss a permit sortie defeats
	ExpBooks       uint32          `json:"drop_expbook"`       // chapterAutoExpBook per permit sortie
	EnemyTimes     uint32          `json:"enemy_times"`        // battles a sortie fights before the boss
	OilLimit       uint32          `json:"oil_limit"`          // oil a permit costs (GetOnceOil)
	TimeRate       float64         `json:"time_rate"`
	TimeCorrection int64           `json:"time_correction"`
}

// chapterTicketItem is an item_virtual_data_statistics row of virtual_type
// 32, 33 or 34: a Handover Permit of a ChapterAutoTicket.TYPE, expiring as
// its drop_arg says (ChapterAutoTicket.GetExpireTimeByArg).
type chapterTicketItem struct {
	kind    uint32
	dropArg json.RawMessage
}

var (
	chapterAutoConfigs map[uint32]chapterAutoConfig
	chapterTicketItems map[uint32]chapterTicketItem
	// gameset auto_battle_time_limit: handover seconds a day
	chapterAutoTimeLimit uint32
	// gameset auto_battle_tickect_to_second_type1 / _type3: seconds a
	// permit adds to the day (CS_13016)
	chapterAutoTicketSeconds map[uint32]uint32
	chapterAutoExpBookMax    uint32 // the bag's max_num of chapterAutoExpBook
)

// item_virtual_data_statistics.virtual_type of the permits (Drop's cases
// 32, 33, 34, model/vo/drop.lua), by ChapterAutoTicket.TYPE
var chapterTicketVirtualTypes = map[uint32]uint32{
	32: chapterTicketWorld,
	33: chapterTicketTime,
	34: chapterTicketMain,
}

func loadChapterAutoData() error {
	var vitems map[uint32]struct {
		VirtualType uint32          `json:"virtual_type"`
		DropArg     json.RawMessage `json:"drop_arg"`
	}
	var items map[uint32]struct {
		MaxNum uint32 `json:"max_num"`
	}
	var gameset map[string]uint32
	err := gamedata.LoadAll(
		gamedata.Load("chapter_auto_statistics", &chapterAutoConfigs),
		gamedata.Load("item_virtual_data_statistics", &vitems),
		gamedata.Load("item_data_statistics", &items),
		loadWorldAutoData,
		func() (err error) {
			gameset, err = gamedata.Gameset()
			return err
		},
	)
	if err != nil {
		return err
	}
	chapterTicketItems = map[uint32]chapterTicketItem{}
	for id, row := range vitems {
		if kind := chapterTicketVirtualTypes[row.VirtualType]; kind != 0 {
			chapterTicketItems[id] = chapterTicketItem{kind: kind, dropArg: row.DropArg}
		}
	}
	values := map[string]*uint32{"auto_battle_time_limit": &chapterAutoTimeLimit}
	var type1, type3 uint32
	values["auto_battle_tickect_to_second_type1"] = &type1
	values["auto_battle_tickect_to_second_type3"] = &type3
	for key, value := range values {
		setting, ok := gameset[key]
		if !ok {
			return gamedata.Missing("gameset", "%s", key)
		}
		*value = setting
	}
	chapterAutoTicketSeconds = map[uint32]uint32{chapterTicketMain: type1, chapterTicketTime: type3}
	chapterAutoExpBookMax = items[chapterAutoExpBook].MaxNum
	return nil
}

// fixTime is ChapterAutoProxy.GetFixTime: the seconds a campaign handover
// sortie takes, the stage's record scaled by time_rate plus
// time_correction (a stage without a row takes its record). The client
// applies it to the records it is sent, so they are stored raw.
func chapterAutoFixTime(chapterID, record uint32) uint32 {
	config, ok := chapterAutoConfigs[chapterID]
	if !ok {
		return record
	}
	return uint32(max(int64(math.Floor(float64(record)*config.TimeRate))+config.TimeCorrection, 1))
}

// chapterTicketExpiry is ChapterAutoTicket.GetExpireTimeByArg: when a
// permit obtained at now expires, from its drop_arg. A date
// ([[y,m,d],[h,m,s]], server time), or a kind and a count: "day" n days
// after the next midnight, "week" n weeks after next Monday, "month" and
// "year" the start of the n+1th next, "act" the end of that activity (0,
// expired, when it isn't running); anything else never expires.
func chapterTicketExpiry(dropArg json.RawMessage, now time.Time) int64 {
	var date [2][3]int
	if json.Unmarshal(dropArg, &date) == nil {
		return time.Date(date[0][0], time.Month(date[0][1]), date[0][2], date[1][0], date[1][1], date[1][2], 0, gamedata.Zone()).Unix()
	}
	var arg []json.RawMessage
	var kind string
	if json.Unmarshal(dropArg, &arg) != nil || len(arg) == 0 || json.Unmarshal(arg[0], &kind) != nil {
		return chapterTicketForever
	}
	n := 0
	if len(arg) > 1 {
		_ = json.Unmarshal(arg[1], &n)
	}
	local := now.In(gamedata.Zone())
	switch kind {
	case "day":
		return nextDay(now).AddDate(0, 0, n).Unix()
	case "week":
		return periodStart(now, week).Add(time.Duration(n+1) * week).Unix()
	case "month":
		return time.Date(local.Year(), local.Month()+time.Month(n)+1, 1, 0, 0, 0, 0, gamedata.Zone()).Unix()
	case "year":
		return time.Date(local.Year()+n+1, 1, 1, 0, 0, 0, 0, gamedata.Zone()).Unix()
	case "act":
		config, open := openActivity(uint32(n), now)
		switch {
		case !open:
			return 0
		case config.Time.Always:
			return chapterTicketForever
		}
		return config.Time.End.Unix()
	}
	return chapterTicketForever
}

// addChapterTickets stores count Handover Permits obtained as the virtual
// item id (grantDrops), expiring as the client works out when it adds them
// (ChapterAutoProxy:AddTicketByItem). One already expired isn't kept.
func (tx *Tx) addChapterTickets(id, count uint32, now time.Time) error {
	item := chapterTicketItems[id]
	expiry := chapterTicketExpiry(item.dropArg, now)
	if expiry < now.Unix() {
		return nil
	}
	return tx.AddChapterAutoTickets(tx.ctx, db.AddChapterAutoTicketsParams{CommanderID: tx.commanderID, Type: item.kind, ExpireAt: expiry, Count: count})
}

// validChapterTickets are the commander's permits not expired at now, the
// soonest to expire first.
func validChapterTickets(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) ([]db.ChapterAutoTicket, error) {
	if err := queries.DeleteExpiredChapterAutoTickets(ctx, db.DeleteExpiredChapterAutoTicketsParams{CommanderID: commanderID, ExpireAt: now.Unix()}); err != nil {
		return nil, err
	}
	return queries.ListChapterAutoTickets(ctx, commanderID)
}

// spendChapterTickets takes count permits of a kind, the soonest to expire
// first as ChapterAutoProxy:ReduceTicketByType does, and returns each one's
// expiry; ErrNotEnough when there are fewer.
func (tx *Tx) spendChapterTickets(kind, count uint32, now time.Time) ([]int64, error) {
	if count == 0 {
		return nil, nil
	}
	tickets, err := validChapterTickets(tx.ctx, tx.Queries, tx.commanderID, now)
	if err != nil {
		return nil, err
	}
	var spent []int64
	for _, ticket := range tickets {
		if ticket.Type != kind || uint32(len(spent)) == count {
			continue
		}
		take := min(ticket.Count, count-uint32(len(spent)))
		for range take {
			spent = append(spent, ticket.ExpireAt)
		}
		err := tx.SetChapterAutoTicketCount(tx.ctx, db.SetChapterAutoTicketCountParams{
			Count: ticket.Count - take, CommanderID: tx.commanderID, Type: kind, ExpireAt: ticket.ExpireAt,
		})
		if err != nil {
			return nil, err
		}
	}
	if uint32(len(spent)) < count {
		return nil, ErrNotEnough
	}
	return spent, nil
}

// chapterAutoState is the commander's refunded oil and today's handover
// time: the time used and added count for the server day they were
// recorded on, and start again from 0 at midnight
// (ChapterAutoProxy:ResetDailyData).
func chapterAutoState(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (db.ChapterAutoState, error) {
	state, err := queries.GetChapterAutoState(ctx, commanderID)
	if errors.Is(err, sql.ErrNoRows) {
		state, err = db.ChapterAutoState{CommanderID: commanderID}, nil
	}
	if err != nil {
		return state, err
	}
	if today := startOfDay(now).Unix(); state.Day != today {
		state.TimeUsed, state.ExtraTime, state.Day = 0, 0, today
	}
	return state, nil
}

func saveChapterAutoState(ctx context.Context, queries *db.Queries, state db.ChapterAutoState) error {
	return queries.SaveChapterAutoState(ctx, db.SaveChapterAutoStateParams(state))
}

// chapterAutoRemaining is ChapterAutoProxy:GetRemainTime: the day's
// allowance (auto_battle_time_limit plus what permits added) less what was
// used, negative once the last sortie ran over.
func chapterAutoRemaining(state db.ChapterAutoState) int64 {
	return int64(chapterAutoTimeLimit) + int64(state.ExtraTime) - int64(state.TimeUsed)
}

func chapterAutoBattle(row db.ChapterAutoBattle) *protobuf.CHAPTER_AUTO_BATTLE {
	return &protobuf.CHAPTER_AUTO_BATTLE{
		Type:       proto.Uint32(row.Type),
		Id:         proto.Uint32(row.ChapterID),
		Time:       proto.Uint32(uint32(row.FinishAt)),
		TicketTime: proto.Uint32(uint32(row.TicketExpireAt)),
		Seconds:    proto.Uint32(row.Seconds),
	}
}

// chapterAutoRunning tells whether a campaign handover is running, which
// keeps the commander out of the campaign (StartChapterAutoCommand
// CheckOccupied) until its rewards are claimed.
func chapterAutoRunning(ctx context.Context, queries *db.Queries, commanderID uint32) (bool, error) {
	battles, err := queries.ListChapterAutoBattles(ctx, commanderID)
	if err != nil {
		return false, err
	}
	for _, battle := range battles {
		if battle.Type == chapterAutoCampaign {
			return true, nil
		}
	}
	return false, nil
}

// recordChapterAuto is the record a won sortie sets when the client ends it
// on uninterrupted Auto-Repeat (CS_13103 OpRetreat with act_arg_1 1,
// ChapterOpCommand: ContinuousOperationRuntimeData:IsRecordTime): the
// seconds since the sortie started, kept when it beats the stage's record.
// The client takes the reply's auto_battle_time_update as the new record
// (ChapterLevelData:retreat, ChapterAutoProxy:UpdateRecord), keeping the
// best too. The client's own measure leaves out the main fleet's volleys;
// the server only sees the sortie's start and end.
func recordChapterAuto(tx *Tx, session *chapterSession, now time.Time) (uint32, error) {
	seconds := uint32(max(now.Unix()-int64(session.info.GetStartTime()), 1))
	err := tx.SaveChapterAutoRecord(tx.ctx, db.SaveChapterAutoRecordParams{
		CommanderID: tx.commanderID, Type: chapterAutoCampaign, ChapterID: session.template.id, Seconds: seconds,
	})
	return seconds, err
}

// fillChapterAuto adds the handover to SC_13001 (ChapterAutoProxy's 13001
// listener): the stage records, the permits not expired, the sorties of the
// running handover, the refunded oil, and today's time used (time_acc) and
// added by permits (extra_time_max).
func fillChapterAuto(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time, msg *protobuf.SC_13001) error {
	records, err := queries.ListChapterAutoRecords(ctx, commanderID)
	if err != nil {
		return err
	}
	for _, record := range records {
		msg.ChapterAutoRecordList = append(msg.ChapterAutoRecordList, &protobuf.CHAPTER_AUTO_RECORD{
			Type: proto.Uint32(record.Type), Id: proto.Uint32(record.ChapterID), Seconds: proto.Uint32(record.Seconds),
		})
	}
	tickets, err := validChapterTickets(ctx, queries, commanderID, now)
	if err != nil {
		return err
	}
	for _, ticket := range tickets {
		msg.ChapterAutoTicketList = append(msg.ChapterAutoTicketList, &protobuf.CHAPTER_AUTO_TICKET{
			Type: proto.Uint32(ticket.Type), Time: proto.Uint32(uint32(ticket.ExpireAt)), Num: proto.Uint32(ticket.Count),
		})
	}
	battles, err := queries.ListChapterAutoBattles(ctx, commanderID)
	if err != nil {
		return err
	}
	for _, battle := range battles {
		msg.ChapterAutoBattleList = append(msg.ChapterAutoBattleList, chapterAutoBattle(battle))
	}
	state, err := chapterAutoState(ctx, queries, commanderID, now)
	if err != nil {
		return err
	}
	msg.Oil = proto.Uint32(state.Oil)
	msg.TimeAcc = proto.Uint32(state.TimeUsed)
	msg.ExtraTimeMax = proto.Uint32(state.ExtraTime)
	return nil
}

// handleStartChapterAuto starts a handover (StartChapterAutoCommand,
// CS_13012): num sorties of campaign stage id one after the other, the
// first ticket_num of them on a Handover Permit. The client checks, and the
// server again: the stage is cleared with a record
// (LevelInfoView's auto button), no handover is running, today's time left
// is positive and above the record times num-1 (the last sortie may run
// over), there are ticket_num campaign permits and the oil for them
// (oil_limit each, the refunded oil going first). On result 0 the client
// lists the reply's sorties, adds their seconds to the day's time used,
// takes the permits (soonest to expire first) and the oil itself. Each
// sortie's ticket_time is the expiry of the permit it took, 0 without one.
// Non-zero shows chapter_auto_start_fail.
func (game *Game) handleStartChapterAuto(client *connection.Client, req *protobuf.CS_13012) error {
	ctx := context.Background()
	now := time.Now()
	var reply protobuf.SC_13013
	err := game.tx(ctx, client, func(tx *Tx) error {
		var err error
		reply.ChapterAutoBattleList, err = tx.startChapterAuto(req, now)
		return err
	})
	if refused(err) {
		return client.Send(&protobuf.SC_13013{Result: result(err)})
	}
	if err != nil {
		return err
	}
	reply.Result = result(nil)
	return client.Send(&reply)
}

func (tx *Tx) startChapterAuto(req *protobuf.CS_13012, now time.Time) ([]*protobuf.CHAPTER_AUTO_BATTLE, error) {
	id, num, tickets := req.GetId(), req.GetNum(), req.GetTicketNum()
	config, ok := chapterAutoConfigs[id]
	if req.GetType() != chapterAutoCampaign || !ok || chapterTemplates[id] == nil || num == 0 || tickets > num {
		return nil, errRefused
	}
	// any handover, an Operation Siren one too (LevelInfoView only offers
	// the button without one, GetCommissionDoingType)
	running, err := tx.ListChapterAutoBattles(tx.ctx, tx.commanderID)
	if err != nil || len(running) > 0 {
		return nil, cmp.Or(err, errRefused)
	}
	progress, err := chapterProgress(tx.ctx, tx.Queries, tx.commanderID, id)
	if err != nil {
		return nil, err
	}
	record, err := tx.GetChapterAutoRecord(tx.ctx, db.GetChapterAutoRecordParams{CommanderID: tx.commanderID, Type: chapterAutoCampaign, ChapterID: id})
	if errors.Is(err, sql.ErrNoRows) || progress.Progress < 100 {
		return nil, errRefused
	}
	if err != nil {
		return nil, err
	}
	seconds := chapterAutoFixTime(id, record)
	state, err := chapterAutoState(tx.ctx, tx.Queries, tx.commanderID, now)
	if err != nil {
		return nil, err
	}
	if remaining := chapterAutoRemaining(state); remaining <= 0 || remaining <= int64(seconds)*int64(num-1) {
		return nil, errRefused
	}
	expiries, err := tx.spendChapterTickets(chapterTicketMain, tickets, now)
	if err != nil {
		return nil, err
	}
	// War Archives tickets for each permit sortie (remaster.go)
	if err := tx.spendRemasterTickets(id, tickets); err != nil {
		return nil, err
	}
	oil := config.OilLimit * tickets
	fromStore := min(state.Oil, oil)
	if err := tx.SpendResource(ResourceOil, oil-fromStore); err != nil {
		return nil, err
	}
	state.Oil -= fromStore
	state.TimeUsed += seconds * num
	if err := saveChapterAutoState(tx.ctx, tx.Queries, state); err != nil {
		return nil, err
	}
	var list []*protobuf.CHAPTER_AUTO_BATTLE
	for i := range num {
		row := db.ChapterAutoBattle{
			CommanderID: tx.commanderID,
			Seq:         i,
			Type:        chapterAutoCampaign,
			ChapterID:   id,
			FinishAt:    now.Unix() + int64(seconds)*int64(i+1),
			Seconds:     seconds,
		}
		if i < uint32(len(expiries)) {
			row.TicketExpireAt = expiries[i]
		}
		if err := tx.AddChapterAutoBattle(tx.ctx, db.AddChapterAutoBattleParams(row)); err != nil {
			return nil, err
		}
		list = append(list, chapterAutoBattle(row))
	}
	return list, nil
}

// handleEndChapterAuto ends the handover, claiming it once all its
// sorties are done or terminating it before (EndChapterAutoCommand,
// CS_13014). num is the client's count of the finished sorties that took a
// permit (what it adds to the stage's War Archives passes); the server
// counts by its own clock. The reply carries:
//   - class_exp, base_class_exp per finished sortie, which the client adds
//     to the Lecture Hall up to its store (NavalAcademyProxy:AddProficiency,
//     warning auto_battle_drop_classEXP_overflow beforehand);
//   - drop_list, a stage clear's rewards per finished permit sortie
//     (scenarioDrops for enemy_times battles and the boss) and its
//     drop_expbook T1 EXP Data Packs, up to the bag's max_num (the client
//     warns auto_battle_drop_bookEXP_overflow, "beyond the limit will be
//     wasted"); the client adds them (PlayerConst.addTranDrop);
//   - for the sorties not finished, given back: their seconds (taken off
//     the day's time used), their permits (chapter_auto_ticket_list, at
//     their expiry; expired ones are lost, auto_battle_drop_book_expired)
//     and those permits' oil, stored for the next handover (oil).
//
// A permit sortie counts as the battles it stands for for the missions.
// An Operation Siren handover is claimed by endWorldChapterAuto.
// Non-zero shows chapter_auto_end_fail.
func (game *Game) handleEndChapterAuto(client *connection.Client, req *protobuf.CS_13014) error {
	ctx := context.Background()
	now := time.Now()
	var reply *protobuf.SC_13015
	err := game.tx(ctx, client, func(tx *Tx) error {
		var err error
		reply, err = tx.endChapterAuto(now)
		return err
	})
	if refused(err) {
		return client.Send(&protobuf.SC_13015{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}

func (tx *Tx) endChapterAuto(now time.Time) (*protobuf.SC_13015, error) {
	battles, err := tx.ListChapterAutoBattles(tx.ctx, tx.commanderID)
	if err != nil {
		return nil, err
	}
	if len(battles) == 0 {
		return nil, errRefused
	}
	if battles[0].Type == chapterAutoWorld {
		return tx.endWorldChapterAuto(battles, now)
	}
	state, err := chapterAutoState(tx.ctx, tx.Queries, tx.commanderID, now)
	if err != nil {
		return nil, err
	}
	reply := &protobuf.SC_13015{Result: result(nil), WorldAp: proto.Uint32(0)}
	var classExp, refundSeconds, refundOil, expBooks uint32
	var drops []*protobuf.DROPINFO
	refunds := map[int64]uint32{}
	var refundOrder []int64
	for _, battle := range battles {
		config := chapterAutoConfigs[battle.ChapterID]
		ticket := battle.TicketExpireAt > 0
		if battle.FinishAt > now.Unix() {
			refundSeconds += battle.Seconds
			if ticket {
				refundOil += config.OilLimit
				// its War Archives tickets (remaster.go)
				if err := tx.refundRemasterTickets(battle.ChapterID, 1); err != nil {
					return nil, err
				}
				if battle.TicketExpireAt >= now.Unix() {
					if refunds[battle.TicketExpireAt] == 0 {
						refundOrder = append(refundOrder, battle.TicketExpireAt)
					}
					refunds[battle.TicketExpireAt]++
				}
			}
			continue
		}
		classExp += config.ClassExp
		if !ticket {
			continue
		}
		// a War Archives clear (remaster.go)
		if err := tx.addRemasterPasses(battle.ChapterID, 1); err != nil {
			return nil, err
		}
		template := chapterTemplates[battle.ChapterID]
		if template == nil {
			continue
		}
		var sortie []*protobuf.DROPINFO
		// the auto repeat doesn't say which enemy each battle was: the
		// chapter's first normal one stands in for them, its award list
		// being what a battle there drops
		enemy := expeditionTemplate{}
		if len(template.enemies) > 0 {
			if known, ok := expeditionTemplates[template.enemies[0].id]; ok {
				enemy = known
			}
		}
		for range config.EnemyTimes {
			sortie = append(sortie, scenarioDrops(template, enemy)...)
		}
		boss := expeditionTemplate{Type: expeditionTypeBoss}
		if ids := gamedata.IDs(config.Boss); len(ids) > 0 {
			if known, ok := expeditionTemplates[ids[0]]; ok && known.isBoss() {
				boss = known
			}
		}
		sortie = append(sortie, scenarioDrops(template, boss)...)
		if err := tx.recordChapterDrops(battle.ChapterID, sortie); err != nil {
			return nil, err
		}
		drops = append(drops, sortie...)
		expBooks += config.ExpBooks
		tx.event(
			TaskEvent{SubType: TaskSubWin, Count: config.EnemyTimes + 1},
			TaskEvent{SubType: TaskSubDefeatBoss, Count: 1},
		)
	}
	if expBooks > 0 {
		owned := uint32(0)
		items, err := tx.ListItems(tx.ctx, tx.commanderID)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if item.ItemID == chapterAutoExpBook {
				owned = item.Count
			}
		}
		if chapterAutoExpBookMax > 0 {
			expBooks = min(expBooks, chapterAutoExpBookMax-min(owned, chapterAutoExpBookMax))
		}
		if expBooks > 0 {
			drops = append(drops, dropInfo(dropTypeItem, chapterAutoExpBook, expBooks))
		}
	}
	reply.DropList = mergeDrops(drops)
	for _, expiry := range refundOrder {
		reply.ChapterAutoTicketList = append(reply.ChapterAutoTicketList, &protobuf.CHAPTER_AUTO_TICKET{
			Type: proto.Uint32(chapterTicketMain), Time: proto.Uint32(uint32(expiry)), Num: proto.Uint32(refunds[expiry]),
		})
		err := tx.AddChapterAutoTickets(tx.ctx, db.AddChapterAutoTicketsParams{
			CommanderID: tx.commanderID, Type: chapterTicketMain, ExpireAt: expiry, Count: refunds[expiry],
		})
		if err != nil {
			return nil, err
		}
	}
	reply.Oil = proto.Uint32(refundOil)
	reply.Seconds = proto.Uint32(refundSeconds)
	reply.ClassExp = proto.Uint32(classExp)
	state.Oil += refundOil
	state.TimeUsed -= min(state.TimeUsed, refundSeconds)
	if err := saveChapterAutoState(tx.ctx, tx.Queries, state); err != nil {
		return nil, err
	}
	if err := tx.DeleteChapterAutoBattles(tx.ctx, tx.commanderID); err != nil {
		return nil, err
	}
	if err := tx.grantDrops(reply.DropList); err != nil {
		return nil, err
	}
	return reply, tx.addFlatClassProficiency(classExp, now)
}

// handleAddChapterAutoTime adds handover time to the day
// (AddChapterAutoTimeCommand, CS_13016): ticket_num_1 campaign permits and
// ticket_num_3 time permits, auto_battle_tickect_to_second_type1 / _type3
// seconds each (an hour). On result 0 the client takes the permits (soonest
// to expire first) and raises the day's allowance itself; non-zero shows
// chapter_auto_add_time_fail.
func (game *Game) handleAddChapterAutoTime(client *connection.Client, req *protobuf.CS_13016) error {
	ctx := context.Background()
	now := time.Now()
	main, extra := req.GetTicketNum_1(), req.GetTicketNum_3()
	err := game.tx(ctx, client, func(tx *Tx) error {
		if main == 0 && extra == 0 {
			return errRefused
		}
		state, err := chapterAutoState(tx.ctx, tx.Queries, tx.commanderID, now)
		if err != nil {
			return err
		}
		if _, err := tx.spendChapterTickets(chapterTicketMain, main, now); err != nil {
			return err
		}
		if _, err := tx.spendChapterTickets(chapterTicketTime, extra, now); err != nil {
			return err
		}
		state.ExtraTime += main*chapterAutoTicketSeconds[chapterTicketMain] + extra*chapterAutoTicketSeconds[chapterTicketTime]
		return saveChapterAutoState(tx.ctx, tx.Queries, state)
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_13017{Result: result(err)})
}
