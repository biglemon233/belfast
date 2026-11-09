package game

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// ChapterConst attachments (CHAPTERCELLINFO_P13.item_type)
const (
	attachNone   = 0
	attachElite  = 4
	attachAmbush = 5
	attachEnemy  = 6
	attachBoss   = 8
)

// ChapterConst cell flags (CHAPTERCELLINFO_P13.item_flag)
const (
	cellFlagActive   = 0
	cellFlagDisabled = 1 // a defeated enemy keeps its cell, disabled
	cellFlagAmbush   = 2 // an ambush waiting for the fleet's answer (OpAmbush)
)

// ChapterConst.Op* (CS_13103.act)
const (
	opRetreat     = 0
	opMove        = 1
	opAmbush      = 4
	opEnemyRound  = 8
	opSubState    = 9
	opSubTeleport = 19 // a submarine fleet jumps to a cell of its hunting range
	opSubStrike   = 31 // the submarine's torpedo strike, worked out client-side
	opRequest     = 49
)

// fullHP is a ship's hp_rant at full health (ten-thousandths).
const fullHP = 10000

// bossSpawnFlag marks the cells the boss can appear on: the capture sends
// flag_list [1] on 1-1's boss cell from the start, it has no client meaning.
const bossSpawnFlag = 1

// chapterDueExtra: the capture's CURRENTCHAPTERINFO.time is start_time plus
// chapter_template.time plus 365 days.
const chapterDueExtra = 365 * 24 * 3600

// chapterOperationItem is the item a sortie can be started with (高效作战指令书,
// item_data_statistics 61001): the sortie's oil and morale cost and its battle
// gains all double, as the item's own display says. Only a loop mode (周回)
// chapter offers it.
const chapterOperationItem = 61001

// chapterSession is the active sortie, stored as the CURRENTCHAPTERINFO the
// client gets plus the scripted ambushes already sprung.
type chapterSession struct {
	template *chapterTemplate
	info     *protobuf.CURRENTCHAPTERINFO
	ambushed db.Cells
	// operationItem is the 高效作战指令书 the sortie runs with, 0 for none:
	// the oil and morale it costs and its battle gains double.
	operationItem uint32
}

// loadChapter returns the commander's active chapter, nil if there is none.
func loadChapter(ctx context.Context, queries *db.Queries, commanderID uint32) (*chapterSession, error) {
	row, err := queries.GetChapterState(ctx, commanderID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info := &protobuf.CURRENTCHAPTERINFO{}
	if err := proto.Unmarshal(row.State, info); err != nil {
		return nil, err
	}
	template := chapterTemplates[info.GetId()]
	if template == nil {
		return nil, fmt.Errorf("active chapter %d has no template", info.GetId())
	}
	return &chapterSession{template: template, info: info, ambushed: row.Ambushed, operationItem: row.OperationItem}, nil
}

func (session *chapterSession) save(ctx context.Context, queries *db.Queries, commanderID uint32) error {
	state, err := proto.Marshal(session.info)
	if err != nil {
		return err
	}
	return queries.SaveChapterState(ctx, db.SaveChapterStateParams{
		CommanderID:   commanderID,
		State:         state,
		Ambushed:      session.ambushed,
		OperationItem: session.operationItem,
	})
}

func cellPos(row, column uint32) *protobuf.CHAPTERCELLPOS_P13 {
	return &protobuf.CHAPTERCELLPOS_P13{Row: proto.Uint32(row), Column: proto.Uint32(column)}
}

func (session *chapterSession) cell(row, column uint32) *protobuf.CHAPTERCELLINFO_P13 {
	for _, cell := range session.info.CellList {
		if cell.GetPos().GetRow() == row && cell.GetPos().GetColumn() == column {
			return cell
		}
	}
	return nil
}

// groups are every fleet of the chapter: the surface ones, then the
// submarines and the support fleets.
func (session *chapterSession) groups() []*protobuf.GROUPINCHAPTER_P13 {
	groups := slices.Clone(session.info.MainGroupList)
	groups = append(groups, session.info.SubmarineGroupList...)
	return append(groups, session.info.SupportGroupList...)
}

// group is the fleet with id, surface or submarine.
func (session *chapterSession) group(id uint32) *protobuf.GROUPINCHAPTER_P13 {
	for _, group := range session.groups() {
		if group.GetId() == id {
			return group
		}
	}
	return nil
}

// removeGroup drops the fleet with id from whichever list holds it (a pulled
// out surface fleet, a submarine), true when the chapter had it.
func (session *chapterSession) removeGroup(id uint32) bool {
	for _, list := range []*[]*protobuf.GROUPINCHAPTER_P13{&session.info.MainGroupList, &session.info.SubmarineGroupList, &session.info.SupportGroupList} {
		groups, removed := (*list)[:0], false
		for _, group := range *list {
			if group.GetId() == id {
				removed = true
				continue
			}
			groups = append(groups, group)
		}
		*list = groups
		if removed {
			return true
		}
	}
	return false
}

// activeGroup is the fleet a battle is fought with: the one standing on an
// enemy, else the first surface fleet.
func (session *chapterSession) activeGroup() *protobuf.GROUPINCHAPTER_P13 {
	for _, group := range session.groups() {
		if isEnemyCell(session.cell(group.GetPos().GetRow(), group.GetPos().GetColumn())) {
			return group
		}
	}
	if len(session.info.MainGroupList) > 0 {
		return session.info.MainGroupList[0]
	}
	return nil
}

func isEnemyCell(cell *protobuf.CHAPTERCELLINFO_P13) bool {
	if cell == nil || cell.GetItemFlag() == cellFlagDisabled {
		return false
	}
	switch cell.GetItemType() {
	case attachEnemy, attachBoss, attachElite, attachAmbush:
		return true
	}
	return false
}

func setCell(cell *protobuf.CHAPTERCELLINFO_P13, kind, id, flag uint32) {
	cell.ItemType = proto.Uint32(kind)
	cell.ItemId = proto.Uint32(id)
	cell.ItemFlag = proto.Uint32(flag)
	cell.ItemData = proto.Uint32(0)
}

// bossesDown counts the defeated bosses, ChapterConst win condition 1.
func (session *chapterSession) bossesDown() uint32 {
	count := uint32(0)
	for _, cell := range session.info.CellList {
		if cell.GetItemType() == attachBoss && cell.GetItemFlag() == cellFlagDisabled {
			count++
		}
	}
	return count
}

// allEnemiesDown is whether no enemy fleet is left on the map. The third star
// of most chapters (star_require_3 kind 3, "击破所有敌舰) is granted on
// TakeBoxCount, which nothing else sets. Ambush cells don't count: they are
// events, not fleets.
func (session *chapterSession) allEnemiesDown() bool {
	for _, cell := range session.info.CellList {
		if cell.GetItemFlag() == cellFlagDisabled {
			continue
		}
		switch cell.GetItemType() {
		case attachEnemy, attachBoss, attachElite:
			return false
		}
	}
	return true
}

// won is Chapter:CheckChapterWin for the conditions the supported chapters use.
func (session *chapterSession) won() bool {
	for _, condition := range session.template.winConditions {
		switch condition.kind {
		case 1: // bosses defeated
			if session.bossesDown() >= condition.param {
				return true
			}
		case 2: // enemies defeated
			if session.info.GetKillCount() >= condition.param {
				return true
			}
		}
	}
	return false
}

// submarineAmmo is what a sortie's submarine fleets can fire: the sum of the
// ships' ammo (ship_data_statistics.ammo, 2 a submarine), which the client
// computes as ChapterLevelData:getFleetAmmo's submarine maximum.
func submarineAmmo(ctx context.Context, queries *db.Queries, commanderID uint32, teams []*protobuf.TEAM_INFO) (uint32, error) {
	var ids []uint32
	for _, team := range teams {
		ids = append(ids, team.GetShipList()...)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	ships, err := shipsByID(ctx, queries, commanderID, ids)
	if err != nil {
		return 0, err
	}
	ammo := uint32(0)
	for _, ship := range ships {
		ammo += shipStatistics[ship.TemplateID].Ammo
	}
	return ammo, nil
}

// chapterTeams are the fleets a sortie brings (FLEET_INFO): the surface
// fleets (main_team) and the submarine ones (submarine_team). The support team
// is left out until a support stage needs it.
type chapterTeams struct {
	main, submarine []*protobuf.TEAM_INFO
}

// newChapter lays the chapter out (chapter_template.grids): the fleets on the
// born cells, the first wave of enemies (spawnWave), the boss cells empty
// until bossRefresh kills. subAmmo is what the submarine fleets fire.
func newChapter(template *chapterTemplate, teams chapterTeams, subAmmo, now uint32) *protobuf.CURRENTCHAPTERINFO {
	info := &protobuf.CURRENTCHAPTERINFO{
		Id:                    proto.Uint32(template.id),
		Time:                  proto.Uint32(now + template.time + chapterDueExtra),
		Round:                 proto.Uint32(0),
		IsSubmarineAutoAttack: proto.Uint32(1),
		ModelActCount:         proto.Uint32(0),
		LoopFlag:              proto.Uint32(0),
		ChapterHp:             proto.Uint32(0),
		KillCount:             proto.Uint32(0),
		ContinuousKillCount:   proto.Uint32(0),
		MoveStepCount:         proto.Uint32(0),
		StartTime:             proto.Uint32(now),
	}
	var born, subBorn []*protobuf.CHAPTERCELLINFO_P13
	for _, grid := range template.grids {
		cell := &protobuf.CHAPTERCELLINFO_P13{Pos: cellPos(grid.row, grid.column)}
		setCell(cell, attachNone, 0, 0)
		// the client lists the grids from the end, so does the capture
		info.CellList = append([]*protobuf.CHAPTERCELLINFO_P13{cell}, info.CellList...)
		switch grid.kind {
		case gridBorn:
			born = append(born, cell)
		case gridSubBorn:
			subBorn = append(subBorn, cell)
		case gridBoss:
			info.CellFlagList = append(info.CellFlagList, &protobuf.CELLFLAG{Pos: cellPos(grid.row, grid.column), FlagList: []uint32{bossSpawnFlag}})
		}
	}
	(&chapterSession{template: template, info: info}).spawnWave()
	// a submarine fleet starts on a submarine born cell (grid kind 16): the
	// chapters with submarine slots have one per slot, and the classes
	// without them fall back to the surface born cells
	if len(subBorn) == 0 {
		subBorn = born
	}
	info.MainGroupList = chapterGroups(teams.main, born, template.ammoTotal)
	info.SubmarineGroupList = chapterGroups(teams.submarine, subBorn, subAmmo)
	shipCount := uint32(0)
	for _, group := range append(slices.Clone(info.MainGroupList), info.SubmarineGroupList...) {
		shipCount += uint32(len(group.GetShipList()))
	}
	info.InitShipCount = proto.Uint32(shipCount)
	return info
}

// chapterGroups are the groups of one team list, in the order the client sent
// them, each on the born cell of its index (cycling when the chapter has fewer
// born cells than fleets).
func chapterGroups(teams []*protobuf.TEAM_INFO, born []*protobuf.CHAPTERCELLINFO_P13, ammo uint32) []*protobuf.GROUPINCHAPTER_P13 {
	groups := make([]*protobuf.GROUPINCHAPTER_P13, 0, len(teams))
	for i, team := range teams {
		pos := cellPos(0, 0)
		if len(born) > 0 {
			cell := born[i%len(born)]
			pos = cellPos(cell.GetPos().GetRow(), cell.GetPos().GetColumn())
		}
		group := &protobuf.GROUPINCHAPTER_P13{
			Id:            proto.Uint32(team.GetId()),
			Pos:           pos,
			StepCount:     proto.Uint32(0),
			Bullet:        proto.Uint32(ammo),
			StartPos:      cellPos(pos.GetRow(), pos.GetColumn()),
			MoveStepDown:  proto.Uint32(0),
			KillCount:     proto.Uint32(0),
			FleetId:       proto.Uint32(team.GetId()),
			VisionLv:      proto.Uint32(0),
			CommanderList: teamMeowfficers(team),
			ShipList:      make([]*protobuf.SHIPINCHAPTER_P13, 0, len(team.GetShipList())),
		}
		for _, id := range team.GetShipList() {
			group.ShipList = append(group.ShipList, &protobuf.SHIPINCHAPTER_P13{Id: proto.Uint32(id), HpRant: proto.Uint32(fullHP)})
		}
		groups = append(groups, group)
	}
	return groups
}

// handleTracking13101 starts a sortie (TrackingCommand, CS_13101): the
// fleets from FLEET_INFO.main_team enter the chapter, chapter_template.oil is
// spent (the client subtracts it too), as are remasterTicketCost War
// Archives tickets for an archive stage, and the map comes back in SC_13102.
// A SelectFleet chapter sends FleetProxy's fleets (Fleet:ChangeToElite, their
// ids), a CustomFleet one its elite fleets (Chapter.PackEliteFleetInfo: id 0,
// padded with empty teams), which are checked and saved like CS_13107's and
// numbered from 1. Each team's commander_main / commander_sub cats go along
// (GROUPINCHAPTER_P13.commander_list, checkTeamMeowfficers). Non-zero
// results make the client show levelScene_tracking_erro. No sortie starts
// while an Operational Handover runs (chapterAutoRunning).
func (game *Game) handleTracking13101(client *connection.Client, req *protobuf.CS_13101) error {
	ctx := context.Background()
	template := chapterTemplates[req.GetId()]
	if template == nil {
		return client.Send(&protobuf.SC_13102{Result: result(errRefused)})
	}
	running, err := chapterAutoRunning(ctx, game.DB.Queries, client.CommanderID)
	if err != nil {
		return err
	}
	if running {
		return client.Send(&protobuf.SC_13102{Result: result(errRefused)})
	}
	var elite *protobuf.FLEET_INFO
	var teams chapterTeams
	if template.fleetType == chapterCustomFleet {
		var err error
		elite, err = checkEliteFleet(ctx, game.DB.Queries, client.CommanderID, template, req.GetFleet())
		if refused(err) {
			return client.Send(&protobuf.SC_13102{Result: result(err)})
		}
		if err != nil {
			return err
		}
		for _, team := range elite.GetMainTeam() {
			if len(team.GetShipList()) > 0 {
				teams.main = append(teams.main, &protobuf.TEAM_INFO{
					Id: proto.Uint32(uint32(len(teams.main) + 1)), ShipList: team.GetShipList(),
					CommanderMain: team.CommanderMain, CommanderSub: team.CommanderSub,
				})
			}
		}
		for _, team := range elite.GetSubmarineTeam() {
			if len(team.GetShipList()) > 0 {
				teams.submarine = append(teams.submarine, &protobuf.TEAM_INFO{
					Id: proto.Uint32(uint32(len(teams.main) + len(teams.submarine) + 1)), ShipList: team.GetShipList(),
					CommanderMain: team.CommanderMain, CommanderSub: team.CommanderSub,
				})
			}
		}
	} else {
		teams.main = req.GetFleet().GetMainTeam()
		teams.submarine = req.GetFleet().GetSubmarineTeam()
		var ids []uint32
		for _, team := range append(slices.Clone(teams.main), teams.submarine...) {
			ids = append(ids, team.GetShipList()...)
		}
		owned, err := ownsShips(ctx, game.DB.Queries, client.CommanderID, ids)
		if err != nil {
			return err
		}
		if !owned || len(slices.Compact(slices.Sorted(slices.Values(ids)))) != len(ids) {
			return client.Send(&protobuf.SC_13102{Result: result(errRefused)})
		}
		err = checkTeamMeowfficers(ctx, game.DB.Queries, client.CommanderID, append(slices.Clone(teams.main), teams.submarine...), false)
		if refused(err) {
			return client.Send(&protobuf.SC_13102{Result: result(err)})
		}
		if err != nil {
			return err
		}
	}
	if len(teams.main) == 0 {
		return client.Send(&protobuf.SC_13102{Result: result(errRefused)})
	}
	if uint32(len(teams.submarine)) > template.submarineNum {
		return client.Send(&protobuf.SC_13102{Result: result(errRefused)})
	}
	// a submarine fleet's torpedoes are its ships' ammo, which the client
	// adds up too (ChapterLevelData:getFleetAmmo, Ship:getShipAmmo) on top of
	// the chapter's ammo_submarine (0 in every table so far)
	subAmmo, err := submarineAmmo(ctx, game.DB.Queries, client.CommanderID, teams.submarine)
	if err != nil {
		return err
	}
	subAmmo += template.ammoSubmarine
	session := &chapterSession{template: template, info: newChapter(template, teams, subAmmo, uint32(time.Now().Unix()))}
	// a cleared stage runs in loop mode (周回模式): the client rolls no
	// ambushes there (ChapterLevelData:activateAmbush is `not isLoop() and
	// ...`) and only then offers Auto-Fight (CanActivateAutoFight requires
	// isLoop())
	progress, err := chapterProgress(ctx, game.DB.Queries, client.CommanderID, template.id)
	if err != nil {
		return err
	}
	if progress.Progress >= 100 {
		session.info.LoopFlag = proto.Uint32(1)
	}
	// the operation item (高效作战指令书): only a loop mode chapter offers it,
	// and it doubles the oil and morale the sortie costs and its battle gains.
	// The client names the item; the bag's own id is what gets spent (one per
	// sortie), so a client that names it differently still pays for it.
	if item := req.GetOperationItem(); item != 0 {
		if session.info.GetLoopFlag() == 0 {
			return client.Send(&protobuf.SC_13102{Result: result(errRefused)})
		}
		if item != chapterOperationItem {
			logger.WithFields("Chapter", logger.FieldValue("item", item)).Warn("unexpected operation item")
		}
		session.operationItem = chapterOperationItem
	}
	// the fleet roles of the fleet select screen (道中战斗 / 旗舰战斗 / 全部战斗 /
	// 待机), keyed by fleet (CS_13101.fleet_duties): Auto-Fight sends the fleets
	// by them, and the client keeps them in CURRENTCHAPTERINFO.fleet_duties, so
	// they have to come back with the map or every sortie runs on the defaults.
	session.info.FleetDuties = req.GetFleetDuties()
	duties := make([]string, 0, len(req.GetFleetDuties()))
	for _, duty := range req.GetFleetDuties() {
		duties = append(duties, fmt.Sprintf("%d=%d", duty.GetKey(), duty.GetValue()))
	}
	logger.WithFields("Chapter",
		logger.FieldValue("id", template.id),
		logger.FieldValue("loop", session.info.GetLoopFlag()),
		logger.FieldValue("item", req.GetOperationItem()),
		logger.FieldValue("duties", fmt.Sprint(duties)),
	).Debug("chapter tracking")
	err = game.tx(ctx, client, func(tx *Tx) error {
		oil := template.oil
		if session.operationItem != 0 {
			oil *= 2
			if err := tx.SpendItem(session.operationItem, 1); err != nil {
				return err
			}
		}
		if err := tx.SpendResource(ResourceOil, oil); err != nil {
			return err
		}
		// War Archives stages take tickets (remaster.go)
		if err := tx.spendRemasterTickets(template.id, 1); err != nil {
			return err
		}
		if elite != nil {
			if err := saveEliteFleet(ctx, tx.Queries, client.CommanderID, elite); err != nil {
				return err
			}
		}
		return session.save(ctx, tx.Queries, client.CommanderID)
	})
	if refused(err) {
		return client.Send(&protobuf.SC_13102{Result: result(errRefused)})
	}
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_13102{Result: result(nil), CurrentChapter: session.info})
}

// opReply is the SC_13104 every op gets: the capture always sets
// auto_battle_time_update (0, no auto battle).
func opReply(err error) *protobuf.SC_13104 {
	return &protobuf.SC_13104{Result: result(err), AutoBattleTimeUpdate: proto.Uint32(0)}
}

// handleChapterOp runs a map action (ChapterOpCommand, CS_13103). act is a
// ChapterConst.Op*, group_id the fleet. A non-zero result makes the client
// show levelScene_operation and resync with OpRequest.
func (game *Game) handleChapterOp(client *connection.Client, req *protobuf.CS_13103) error {
	ctx := context.Background()
	session, err := loadChapter(ctx, game.DB.Queries, client.CommanderID)
	if err != nil {
		return err
	}
	if session == nil {
		return client.Send(opReply(errRefused))
	}
	reply := opReply(nil)
	logger.WithFields("Chapter",
		logger.FieldValue("act", req.GetAct()),
		logger.FieldValue("group", req.GetGroupId()),
		logger.FieldValue("arg1", req.GetActArg_1()),
		logger.FieldValue("arg2", req.GetActArg_2()),
	).Debug("chapter op")
	switch req.GetAct() {
	case opRetreat:
		return game.retreat(ctx, client, session, req.GetGroupId(), req.GetActArg_1() == 1)
	case opMove:
		reply = session.move(req.GetGroupId(), req.GetActArg_1(), req.GetActArg_2())
	case opAmbush:
		var fleet fleetEvasion
		if group := session.group(req.GetGroupId()); group != nil && req.GetActArg_1() == 1 {
			if fleet, err = groupEvasion(ctx, game.DB.Queries, client.CommanderID, group); err != nil {
				return err
			}
		}
		reply = session.answerAmbush(req.GetGroupId(), req.GetActArg_1(), fleet)
	case opEnemyRound:
		session.info.Round = proto.Uint32(session.info.GetRound() + 1)
	case opSubState:
		session.info.IsSubmarineAutoAttack = proto.Uint32(req.GetActArg_1())
	case opSubTeleport:
		reply = session.teleportSub(req.GetGroupId(), req.GetActArg_1(), req.GetActArg_2())
	case opSubStrike:
		// the client works the submarine's torpedo strike out itself
		// (OPSubStrike only needs result 0); its ammo is its own bookkeeping
	case opRequest:
		reply = session.resync(req.GetGroupId())
	}
	if reply.GetResult() == 0 {
		if err := session.save(ctx, game.DB.Queries, client.CommanderID); err != nil {
			return err
		}
	}
	return client.Send(reply)
}

// retreat ends the sortie (OpRetreat, group 0) or pulls one fleet out. Ending
// a won chapter (ChapterLevelData:UpdateProgressOnRetreat) adds progress_boss
// to its progress, counts a clear (also towards the War Archives awards,
// addRemasterPasses), and a pass when it was already cleared.
// A win the client ends on uninterrupted Auto-Repeat (act_arg_1 1) sets the
// stage's Operational Handover record (recordChapterAuto), sent back as
// auto_battle_time_update.
func (game *Game) retreat(ctx context.Context, client *connection.Client, session *chapterSession, groupID uint32, autoRepeat bool) error {
	if groupID != 0 {
		// pulling a fleet out: the sortie goes on while a surface fleet is
		// left (pulling the last one, or one the chapter doesn't know, ends
		// it below)
		if session.removeGroup(groupID) && len(session.info.MainGroupList) > 0 {
			if err := session.save(ctx, game.DB.Queries, client.CommanderID); err != nil {
				return err
			}
			return client.Send(opReply(nil))
		}
	}
	reply := opReply(nil)
	err := game.tx(ctx, client, func(tx *Tx) error {
		queries := tx.Queries
		if session.won() {
			// the commander manual's daily 7205: every hard mode win counts,
			// not only the one that finishes the map (困难海域)
			if session.template.hard {
				tx.event(TaskEvent{SubType: TaskSubHardClear, Count: 1})
			}
			if autoRepeat {
				seconds, err := recordChapterAuto(tx, session, time.Now())
				if err != nil {
					return err
				}
				reply.AutoBattleTimeUpdate = proto.Uint32(seconds)
			}
			progress, err := chapterProgress(ctx, queries, client.CommanderID, session.template.id)
			if err != nil {
				return err
			}
			if progress.Progress >= 100 {
				progress.PassCount++
			}
			if progress.Progress < 100 && progress.Progress+session.template.progressBoss >= 100 {
				tx.event(TaskEvent{SubType: TaskSubClearStage, Target: session.template.id, Count: 1})
			}
			progress.Progress = min(progress.Progress+session.template.progressBoss, 100)
			progress.DefeatCount++
			progress.TodayDefeatCount++
			if session.allEnemiesDown() {
				progress.TakeBoxCount = 1
			}
			if err := queries.SaveChapterProgress(ctx, db.SaveChapterProgressParams(progress)); err != nil {
				return err
			}
			// War Archives clear awards (remaster.go)
			if err := tx.addRemasterPasses(session.template.id, 1); err != nil {
				return err
			}
			// the side tasks' stars (TaskSubChapterStars) and the manual's
			// state tasks after the clear
			if err := tx.syncStateTasks(); err != nil {
				return err
			}
		}
		return queries.DeleteChapterState(ctx, client.CommanderID)
	})
	if err != nil {
		return err
	}
	return client.Send(reply)
}

// move walks a fleet to (row, column) (OpMove). The path goes around enemies
// (it may end on one, the client then starts the battle) and stops early when
// an ambush springs; the client animates move_path and merges map_update.
func (session *chapterSession) move(groupID, row, column uint32) *protobuf.SC_13104 {
	group := session.group(groupID)
	if group == nil {
		return opReply(errRefused)
	}
	path := session.findPath(group.GetPos().GetRow(), group.GetPos().GetColumn(), row, column)
	if path == nil {
		return opReply(errRefused)
	}
	reply := opReply(nil)
	for _, step := range path {
		reply.MovePath = append(reply.MovePath, cellPos(step[0], step[1]))
		if cell := session.springAmbush(step[0], step[1]); cell != nil {
			reply.MapUpdate = append(reply.MapUpdate, proto.CloneOf(cell))
			break
		}
	}
	last := reply.MovePath[len(reply.MovePath)-1]
	steps := uint32(len(reply.MovePath))
	group.Pos = cellPos(last.GetRow(), last.GetColumn())
	group.StepCount = proto.Uint32(group.GetStepCount() + steps)
	session.info.MoveStepCount = proto.Uint32(session.info.GetMoveStepCount() + steps)
	session.info.Round = proto.Uint32(session.info.GetRound() + 1)
	return reply
}

// teleportSub jumps a submarine fleet to (row, column) (OpSubTeleport, its
// hunting range's teleport). The client builds the jump's path from the
// fleet's start_pos itself, so only the position and the sortie's counters
// change.
func (session *chapterSession) teleportSub(groupID, row, column uint32) *protobuf.SC_13104 {
	group := session.group(groupID)
	submarine := slices.ContainsFunc(session.info.SubmarineGroupList, func(candidate *protobuf.GROUPINCHAPTER_P13) bool {
		return candidate.GetId() == groupID
	})
	if group == nil || !submarine || !session.walkable(row, column) {
		return opReply(errRefused)
	}
	group.Pos = cellPos(row, column)
	session.info.MoveStepCount = proto.Uint32(session.info.GetMoveStepCount() + 1)
	session.info.Round = proto.Uint32(session.info.GetRound() + 1)
	return opReply(nil)
}

// walkable is whether the chapter's grids let a fleet stand on (row, column).
func (session *chapterSession) walkable(row, column uint32) bool {
	for _, grid := range session.template.grids {
		if grid.row == row && grid.column == column {
			return grid.walkable
		}
	}
	return false
}

// findPath is a shortest walk over walkable cells, without the start cell.
// Cells holding an enemy can only be the destination.
func (session *chapterSession) findPath(fromRow, fromColumn, toRow, toColumn uint32) [][2]uint32 {
	walkable := map[[2]uint32]bool{}
	for _, grid := range session.template.grids {
		walkable[[2]uint32{grid.row, grid.column}] = grid.walkable
	}
	start, goal := [2]uint32{fromRow, fromColumn}, [2]uint32{toRow, toColumn}
	if !walkable[goal] || start == goal {
		return nil
	}
	previous := map[[2]uint32][2]uint32{start: start}
	queue := [][2]uint32{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == goal {
			var path [][2]uint32
			for step := goal; step != start; step = previous[step] {
				path = append([][2]uint32{step}, path...)
			}
			return path
		}
		if current != start && isEnemyCell(session.cell(current[0], current[1])) {
			continue
		}
		for _, delta := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
			next := [2]uint32{uint32(int(current[0]) + delta[0]), uint32(int(current[1]) + delta[1])}
			if _, seen := previous[next]; seen || !walkable[next] {
				continue
			}
			previous[next] = current
			queue = append(queue, next)
		}
	}
	return nil
}

// springAmbush rolls Chapter:getAmbushRate on a cell the fleet walks
// through: 5% plus the matching ambush_ratio_extra rows (1-1's (4,3) is
// certain). The investigation term needs fleet stats the server doesn't
// model, so it is left out; a sprung cell doesn't spring again this sortie.
// A loop mode (周回) chapter rolls none at all, as in the game
// (ChapterLevelData:activateAmbush is gated on not isLoop()): the client
// shows its ambush prompt for any ambush cell the server sends, loop mode or
// not, and Auto-Fight then waits for a tap that would also void the
// continuous operation's record.
func (session *chapterSession) springAmbush(row, column uint32) *protobuf.CHAPTERCELLINFO_P13 {
	if !session.template.isAmbush || len(session.template.ambushes) == 0 || session.info.GetLoopFlag() != 0 {
		return nil
	}
	cell := session.cell(row, column)
	if cell == nil || cell.GetItemType() != attachNone {
		return nil
	}
	key := [2]uint32{row, column}
	if slices.Contains(session.ambushed, key) {
		return nil
	}
	rate := 500
	for _, extra := range session.template.ambushRatios {
		if extra.anyCell || (extra.row == row && extra.column == column) {
			rate += extra.ratio
		}
	}
	if rate <= 0 || rand.IntN(10000) >= rate {
		return nil
	}
	session.ambushed = append(session.ambushed, key)
	setCell(cell, attachAmbush, session.template.ambushes[rand.IntN(len(session.template.ambushes))], cellFlagAmbush)
	return cell
}

// scriptedAmbush is whether a cell has its own ambush_ratio_extra row.
func (session *chapterSession) scriptedAmbush(row, column uint32) bool {
	for _, extra := range session.template.ambushRatios {
		if !extra.anyCell && extra.row == row && extra.column == column {
			return true
		}
	}
	return false
}

// fleetEvasion is what Chapter:getAmbushDodge reads of a fleet
// (model/vo/chaptercell/chapterfleet.lua).
type fleetEvasion struct {
	dodgeSums   float64 // getDodgeSums
	equipRateUp float64 // getEquipDodgeRateUp
}

// groupEvasion is a chapter fleet's fleetEvasion: getDodgeSums is the sum of
// the evasion of its ships still standing (hp_rant > 0, getShips(false)),
// with what the fleet's cats add (getProperties(getCommanders())), to the
// power 2/3, getEquipDodgeRateUp the best equip_parameters.avoid_extra
// among the active equipment of all its ships.
func groupEvasion(ctx context.Context, queries *db.Queries, commanderID uint32, group *protobuf.GROUPINCHAPTER_P13) (fleetEvasion, error) {
	var ids []uint32
	for _, ship := range group.GetShipList() {
		ids = append(ids, ship.GetId())
	}
	ships, err := shipsByID(ctx, queries, commanderID, ids)
	if err != nil {
		return fleetEvasion{}, err
	}
	cats, err := groupMeowfficers(ctx, queries, commanderID, group)
	if err != nil {
		return fleetEvasion{}, err
	}
	sum, avoidExtra := 0.0, 0.0
	for _, member := range group.GetShipList() {
		ship, ok := ships[member.GetId()]
		if !ok {
			continue
		}
		rows, err := queries.ListShipEquipments(ctx, ship.ID)
		if err != nil {
			return fleetEvasion{}, err
		}
		slots := make([]uint32, shipEquipSlots)
		for _, row := range rows {
			if row.Pos >= 1 && row.Pos <= shipEquipSlots {
				slots[row.Pos-1] = row.EquipID
			}
		}
		for _, equip := range activeEquipments(slots) {
			avoidExtra = max(avoidExtra, equipEvasions[equip].avoidExtra)
		}
		if member.GetHpRant() > 0 {
			retrofits, err := queries.ListShipTransforms(ctx, ship.ID)
			if err != nil {
				return fleetEvasion{}, err
			}
			sum += shipDodge(ship, slots, retrofits, meowfficerDodge(cats, ship))
		}
	}
	return fleetEvasion{dodgeSums: math.Pow(sum, 2.0/3), equipRateUp: avoidExtra / 10000}, nil
}

// ambushDodge is Chapter:getAmbushDodge (model/vo/chapterleveldata.lua), the
// chance the ambush window shows: dodgeSums / (dodgeSums + avoid_ratio),
// plus the equipment's evasion bonus unless the cell has its own non-zero
// ambush_ratio_extra row, within [0, 1].
func (session *chapterSession) ambushDodge(row, column uint32, fleet fleetEvasion) float64 {
	rate := 0.0
	if total := fleet.dodgeSums + float64(session.template.avoidRatio); total > 0 {
		rate = fleet.dodgeSums / total
	}
	cellRatio := 0
	for _, extra := range session.template.ambushRatios {
		if !extra.anyCell && extra.row == row && extra.column == column {
			cellRatio = extra.ratio
			break
		}
	}
	if cellRatio == 0 {
		rate += fleet.equipRateUp
	}
	return min(max(rate, 0), 1)
}

// answerAmbush is OpAmbush: arg 1 tries to evade, 0 fights. An evaded ambush
// is cleared (map_update with item_type 0, the client shows
// chapter_tip_aovid_succeed), otherwise it turns into an active enemy the
// client battles. Evading succeeds with the chance ambushDodge gives, except
// on the scripted tutorial ambush (1-1's cell with its own
// ambush_ratio_extra row), always evaded as in the capture: the new guide
// (SG002) taps dodge_button there (new.NS024), then says the ambush was shaken
// off (new.NS024_1) and points at the next enemy's cell (new.NS025), so it
// expects the evasion to succeed.
func (session *chapterSession) answerAmbush(groupID, evade uint32, fleet fleetEvasion) *protobuf.SC_13104 {
	group := session.group(groupID)
	if group == nil {
		return opReply(errRefused)
	}
	row, column := group.GetPos().GetRow(), group.GetPos().GetColumn()
	cell := session.cell(row, column)
	if cell == nil || cell.GetItemType() != attachAmbush || cell.GetItemFlag() != cellFlagAmbush {
		return opReply(errRefused)
	}
	if evade == 1 && (session.scriptedAmbush(row, column) || rand.Float64() < session.ambushDodge(row, column, fleet)) {
		setCell(cell, attachNone, 0, 0)
	} else {
		cell.ItemFlag = proto.Uint32(cellFlagActive)
	}
	reply := opReply(nil)
	reply.MapUpdate = []*protobuf.CHAPTERCELLINFO_P13{proto.CloneOf(cell)}
	return reply
}

// resync is OpRequest, sent after a failed op: the fleet's position and the
// whole map again.
func (session *chapterSession) resync(groupID uint32) *protobuf.SC_13104 {
	reply := opReply(nil)
	if group := session.group(groupID); group != nil {
		reply.MovePath = []*protobuf.CHAPTERCELLPOS_P13{cellPos(group.GetPos().GetRow(), group.GetPos().GetColumn())}
		reply.ShipUpdate = group.ShipList
	}
	reply.MapUpdate = session.info.CellList
	reply.CellFlagList = session.info.CellFlagList
	return reply
}

// handleChapterBattleResult answers CS_13106 (ChapterBattleResultRequestCommand),
// sent after every chapter battle: SC_13105.map_update carries the cells the
// map gains (ChapterProxy:OnBattleFinished merges them), the next wave of
// enemies once the last one is down and the boss once bossRefresh enemies
// are. The defeated cell itself isn't sent: the client disables it
// (ChapterLevelData:writeBack), and the capture's 1-1 reply only has the boss.
func (game *Game) handleChapterBattleResult(client *connection.Client, req *protobuf.CS_13106) error {
	ctx := context.Background()
	session, err := loadChapter(ctx, game.DB.Queries, client.CommanderID)
	if err != nil {
		return err
	}
	reply := &protobuf.SC_13105{}
	if session != nil {
		for _, cell := range session.spawnWave() {
			reply.MapUpdate = append(reply.MapUpdate, proto.CloneOf(cell))
		}
		if cell := session.spawnBoss(); cell != nil {
			reply.MapUpdate = append(reply.MapUpdate, proto.CloneOf(cell))
		}
		if len(reply.MapUpdate) > 0 {
			if err := session.save(ctx, game.DB.Queries, client.CommanderID); err != nil {
				return err
			}
		}
	}
	return client.Send(reply)
}

// occupied is whether a fleet stands on (row, column).
func (session *chapterSession) occupied(row, column uint32) bool {
	for _, group := range session.groups() {
		if group.GetPos().GetRow() == row && group.GetPos().GetColumn() == column {
			return true
		}
	}
	return false
}

// freeCells are the empty cells of a grid kind no fleet stands on, shuffled.
func (session *chapterSession) freeCells(kind uint32) []*protobuf.CHAPTERCELLINFO_P13 {
	var free []*protobuf.CHAPTERCELLINFO_P13
	for _, grid := range session.template.grids {
		if grid.kind != kind || session.occupied(grid.row, grid.column) {
			continue
		}
		if cell := session.cell(grid.row, grid.column); cell != nil && cell.GetItemType() == attachNone {
			free = append(free, cell)
		}
	}
	rand.Shuffle(len(free), func(i, j int) { free[i], free[j] = free[j], free[i] })
	return free
}

// spawnWave puts the next wave of enemies on the map once none of the
// previous ones is left standing. Wave n is enemy_refresh[n] enemies drawn
// from expedition_id_weight_list on the enemy cells plus elite_refresh[n]
// elites from elite_expedition_list on the elite cells, both AttachEnemy:
// Chapter:getWaveCount (chapterleveldata.lua) counts the AttachEnemy cells on
// enemy and elite grids against the running sum of these to tell which wave
// the map is on. 1-1 has one wave, 1-2 two of one enemy each (boss_refresh 2).
func (session *chapterSession) spawnWave() []*protobuf.CHAPTERCELLINFO_P13 {
	spawned := uint32(0)
	for _, grid := range session.template.grids {
		if grid.kind != gridEnemy && grid.kind != gridElite {
			continue
		}
		cell := session.cell(grid.row, grid.column)
		if cell == nil || cell.GetItemType() != attachEnemy {
			continue
		}
		if cell.GetItemFlag() != cellFlagDisabled {
			return nil // the current wave isn't cleared yet
		}
		spawned++
	}
	wave, total := 0, uint32(0)
	for ; wave < len(session.template.enemyRefresh); wave++ {
		total += uint32(max(session.template.enemyRefresh[wave], 0))
		if wave < len(session.template.eliteRefresh) {
			total += uint32(max(session.template.eliteRefresh[wave], 0))
		}
		if total > spawned {
			break
		}
	}
	if wave >= len(session.template.enemyRefresh) {
		return nil
	}
	var cells []*protobuf.CHAPTERCELLINFO_P13
	for i, cell := range session.freeCells(gridEnemy) {
		if i >= session.template.enemyRefresh[wave] {
			break
		}
		expedition := uint32(0)
		if pick := weightedPick(rand.Uint32N, session.template.enemies, func(enemy weightedExpedition) uint32 { return enemy.weight }); pick >= 0 {
			expedition = session.template.enemies[pick].id
		}
		setCell(cell, attachEnemy, expedition, cellFlagActive)
		cells = append(cells, cell)
	}
	if wave < len(session.template.eliteRefresh) && len(session.template.elites) > 0 {
		for i, cell := range session.freeCells(gridElite) {
			if i >= session.template.eliteRefresh[wave] {
				break
			}
			setCell(cell, attachEnemy, session.template.elites[rand.IntN(len(session.template.elites))], cellFlagActive)
			cells = append(cells, cell)
		}
	}
	return cells
}

// spawnBoss puts the boss on a free boss cell once bossRefresh enemies are
// down (kill_count, which counts every won battle, ambushes included, as the
// client's defeatEnemies does).
func (session *chapterSession) spawnBoss() *protobuf.CHAPTERCELLINFO_P13 {
	if len(session.template.bosses) == 0 || session.info.GetKillCount() < session.template.bossRefresh {
		return nil
	}
	for _, cell := range session.info.CellList {
		if cell.GetItemType() == attachBoss {
			return nil
		}
	}
	free := session.freeCells(gridBoss)
	if len(free) == 0 {
		return nil
	}
	setCell(free[0], attachBoss, session.template.bosses[rand.IntN(len(session.template.bosses))], cellFlagActive)
	return free[0]
}

func chapterProgress(ctx context.Context, queries *db.Queries, commanderID, chapterID uint32) (db.ChapterProgress, error) {
	progress, err := queries.GetChapterProgress(ctx, db.GetChapterProgressParams{CommanderID: commanderID, ChapterID: chapterID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.ChapterProgress{CommanderID: commanderID, ChapterID: chapterID}, nil
	}
	return progress, err
}

// chapterList is SC_13001: the chapters played so far (Chapter.New reads
// CHAPTERINFO), the elite fleets by formation and the War Archives tickets
// (reactChapter, remaster.go); the login adds the Operational Handover
// (fillChapterAuto).
func chapterList(ctx context.Context, queries *db.Queries, commanderID uint32, ships []db.Ship) (*protobuf.SC_13001, error) {
	rows, err := queries.ListChapterProgress(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	fleets, err := eliteFleets(ctx, queries, commanderID, ships)
	if err != nil {
		return nil, err
	}
	// War Archives tickets (remaster.go)
	react, err := reactChapter(ctx, queries, commanderID, time.Now())
	if err != nil {
		return nil, err
	}
	msg := &protobuf.SC_13001{
		ReactChapter: react,
		FleetList:    fleets,
	}
	for _, row := range rows {
		msg.ChapterList = append(msg.ChapterList, &protobuf.CHAPTERINFO{
			Id:               proto.Uint32(row.ChapterID),
			Progress:         proto.Uint32(row.Progress),
			KillBossCount:    proto.Uint32(row.KillBossCount),
			KillEnemyCount:   proto.Uint32(row.KillEnemyCount),
			TakeBoxCount:     proto.Uint32(row.TakeBoxCount),
			DefeatCount:      proto.Uint32(row.DefeatCount),
			TodayDefeatCount: proto.Uint32(row.TodayDefeatCount),
			PassCount:        proto.Uint32(row.PassCount),
		})
	}
	return msg, nil
}

// currentChapter is SC_13000: the sortie in progress, if any, so the client
// goes back into it. Repairs aren't counted yet.
func currentChapter(ctx context.Context, queries *db.Queries, commanderID uint32) (*protobuf.SC_13000, error) {
	msg := &protobuf.SC_13000{}
	session, err := loadChapter(ctx, queries, commanderID)
	if err != nil {
		return nil, err
	}
	if session != nil {
		msg.CurrentChapter = session.info
	}
	return msg, nil
}
