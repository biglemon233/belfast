package game

import (
	"context"
	"math/rand/v2"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// const.lua SYSTEM_*: what a battle is
const (
	systemScenario = 1 // a battle on a campaign map
	systemDuel     = 3 // an exercise
)

// ys.Battle.BattleConst.BattleScore: above C is a win (Chapter:writeBack)
const (
	scoreC = 1
	scoreA = 3
	scoreS = 4
)

// The client never computes battle exp: BattleGateScenario adds the reply's
// ship_exp_list and player_exp (FinishStageCommand.addShipsExp,
// GeneralPlayerCosume) and no table has the rates, which are server-side
// (battleExp, server/battle_exp.json).
const (
	// scenarioExpRate: the capture's ship exp is expedition exp x rank x morale
	// x 2 (x flagship / mvp), its commander exp the wiki's A-rank exp x rank
	// x 2 ("2x" rows); the client data doesn't say where the 2 comes from.
	scenarioExpRate = 2
	maxIntimacy     = 10000
	// scenarioPlayerExp is the commander exp of a won campaign battle in the
	// capture (3 ships, a level 1 and a level 2 stage), what a win gives
	// without server/battle_exp.json.
	scenarioPlayerExp = 65
	// scenarioGold / scenarioBossGold are the coins (Coins, 59001 in the
	// award lists) a won normal / boss battle dropped in the capture; the
	// amount isn't in the client data.
	scenarioGold     = 9
	scenarioBossGold = 44
	// a boss battle's draws from the chapter's award pools (capture: one tech
	// pack, three gear parts, one ship)
	bossTechPacks = 1
	bossGearParts = 3
	bossShips     = 1
	// the draws from a battle's own award list: a normal or elite enemy one,
	// the boss two (its list holds the designs, the packs and the parts)
	battleItemDraws = 1
	bossItemDraws   = 2
)

// handleBeginStage starts a battle (BeginStageCommand.SendRequest, CS_40001).
// key is the token the client mixes into its CS_40003 check value; ships
// lose battle_consume_energy morale (the client does the same on result 0).
// On a campaign map the fleet must stand on the enemy being fought; a daily
// challenge must be playable and costs the fleet's start oil
// (battleStartOil, refused without enough).
func (game *Game) handleBeginStage(client *connection.Client, req *protobuf.CS_40001) error {
	ctx := context.Background()
	if req.GetSystem() == systemDuel {
		return game.beginDuel(client, req)
	}
	if req.GetSystem() == systemGuild {
		return game.beginGuildBoss(client, req)
	}
	if req.GetSystem() == systemChallenge {
		return game.beginChallenge(client, req)
	}
	if req.GetSystem() == systemLimitChallenge {
		return game.beginLimitChallenge(client, req)
	}
	if req.GetSystem() == systemWorldBoss {
		return game.beginWorldBoss(client, req)
	}
	if req.GetSystem() == systemWorld {
		return game.beginWorld(client, req)
	}
	if req.GetSystem() == systemScenario {
		session, err := loadChapter(ctx, game.DB.Queries, client.CommanderID)
		if err != nil {
			return err
		}
		if session == nil || session.battleCell(req.GetData()) == nil {
			return client.Send(&protobuf.SC_40002{Result: result(errRefused)})
		}
	}
	if req.GetSystem() == systemRoutine || req.GetSystem() == systemSubRoutine {
		err := checkDailyBegin(ctx, game.DB.Queries, client.CommanderID, req.GetData(), time.Now())
		if refused(err) {
			return client.Send(&protobuf.SC_40002{Result: result(err)})
		}
		if err != nil {
			return err
		}
	}
	byID, err := shipsByID(ctx, game.DB.Queries, client.CommanderID, req.GetShipIdList())
	if err != nil {
		return err
	}
	err = game.tx(ctx, client, func(tx *Tx) error {
		// a daily challenge costs its start oil now (BattleGateRoutine /
		// BattleGateSubRoutine.Entrance), its end oil on a win (finishDaily)
		if req.GetSystem() == systemRoutine || req.GetSystem() == systemSubRoutine {
			if err := tx.SpendResource(ResourceOil, battleStartOil(byID)); err != nil {
				return err
			}
		}
		for _, id := range req.GetShipIdList() {
			ship, ok := byID[id]
			if !ok {
				continue
			}
			ship.Energy -= min(ship.Energy, battleConsumeEnergy)
			if err := saveShipBattleState(ctx, tx.Queries, ship); err != nil {
				return err
			}
		}
		return nil
	})
	if refused(err) {
		return client.Send(&protobuf.SC_40002{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_40002{Result: result(nil), Key: proto.Uint32(rand.Uint32N(10000))})
}

// battleCell is the cell the active fleet fights on, if it holds expedition id.
func (session *chapterSession) battleCell(id uint32) *protobuf.CHAPTERCELLINFO_P13 {
	group := session.activeGroup()
	if group == nil {
		return nil
	}
	cell := session.cell(group.GetPos().GetRow(), group.GetPos().GetColumn())
	if !isEnemyCell(cell) || cell.GetItemId() != id {
		return nil
	}
	return cell
}

// handleFinishStage settles a battle (FinishStageCommand.SendRequest,
// CS_40003). On a campaign map a win (score above C) disables the enemy's
// cell (an ambush's is cleared), the fleet spends one ammo and keeps its hp.
// The reply's ship_exp_list is exp gained (the client levels ships itself),
// intimacy is 10000 + affinity gained, energy extra morale spent;
// commander_exp the EXP of the fleet's meowfficers (grantBattleMeowfficerExp);
// drop_info is added by the client through PlayerConst.addTranDrop, new
// ships come in SC_12042 right after, then the mailbox counts (SC_30001).
func (game *Game) handleFinishStage(client *connection.Client, req *protobuf.CS_40003) error {
	ctx := context.Background()
	if req.GetSystem() == systemDuel {
		return game.finishDuel(client, req)
	}
	if req.GetSystem() == systemGuild {
		return game.finishGuildBoss(client, req)
	}
	if req.GetSystem() == systemChallenge {
		return game.finishChallenge(client, req)
	}
	if req.GetSystem() == systemLimitChallenge {
		return game.finishLimitChallenge(client, req)
	}
	if req.GetSystem() == systemWorldBoss {
		return game.finishWorldBoss(client, req)
	}
	if req.GetSystem() == systemWorld {
		return game.finishWorld(client, req)
	}
	if req.GetSystem() == systemRoutine || req.GetSystem() == systemSubRoutine {
		return game.finishDaily(client, req)
	}
	if req.GetSystem() != systemScenario {
		return client.Send(&protobuf.SC_40004{Result: result(nil)})
	}
	session, err := loadChapter(ctx, game.DB.Queries, client.CommanderID)
	if err != nil {
		return err
	}
	var cell *protobuf.CHAPTERCELLINFO_P13
	if session != nil {
		cell = session.battleCell(req.GetData())
	}
	expedition, known := expeditionTemplates[req.GetData()]
	if cell == nil || !known {
		return client.Send(&protobuf.SC_40004{Result: result(errRefused)})
	}
	group := session.activeGroup()
	score := min(req.GetScore(), scoreS)
	win := score > scoreC
	if _, known := battleExp.ship[score]; !known {
		logger.WithFields("Battle", logger.FieldValue("score", score)).Warn("no ship exp rate for this score, ships get no exp")
	}

	statShips := make([]uint32, len(req.GetStatistics()))
	for i, stat := range req.GetStatistics() {
		statShips[i] = stat.GetShipId()
	}
	byID, err := shipsByID(ctx, game.DB.Queries, client.CommanderID, statShips)
	if err != nil {
		return err
	}
	commander, err := game.DB.GetCommander(ctx, client.CommanderID)
	if err != nil {
		return err
	}

	reply := &protobuf.SC_40004{Result: result(nil)}
	var events []TaskEvent
	if win {
		events = append(events, TaskEvent{SubType: TaskSubWin, Count: 1})
		if score >= scoreA {
			events = append(events, TaskEvent{SubType: TaskSubWinA, Count: 1})
		}
		if score >= scoreS {
			events = append(events, TaskEvent{SubType: TaskSubWinS, Count: 1})
		}
		if expedition.isBoss() {
			events = append(events, TaskEvent{SubType: TaskSubDefeatBoss, Count: 1})
		}
		if kills := uint32(len(req.GetKillIdList())); kills > 0 {
			events = append(events, TaskEvent{SubType: TaskSubDefeatEnemy, Count: kills})
		}
	}
	mvp := battleMVP(req.GetStatistics(), byID)
	reply.Mvp = proto.Uint32(mvp)
	flagship := uint32(0)
	if len(group.ShipList) > 0 {
		flagship = group.ShipList[0].GetId()
	}
	for _, stat := range req.GetStatistics() {
		for _, inChapter := range group.ShipList {
			if _, ok := byID[stat.GetShipId()]; ok && inChapter.GetId() == stat.GetShipId() {
				inChapter.HpRant = proto.Uint32(min(stat.GetHpRest(), fullHP))
			}
		}
	}
	ships := settleBattleShips(req.GetStatistics(), byID, func(ship db.Ship) uint32 {
		return shipBattleExp(expedition, scenarioExpRate, score, ship, ship.ID == flagship, ship.ID == mvp)
	}, win, flagship, mvp)
	events = append(events, ships.events...)
	reply.ShipExpList = ships.exps
	updated, proficiency, techLevel := ships.updated, ships.proficiency, ships.techLevel
	group.Bullet = proto.Uint32(group.GetBullet() - min(group.GetBullet(), 1))
	reply.PlayerExp = proto.Uint32(playerBattleExp(expedition, score, uint32(len(updated)), commander.Level))

	if win {
		if cell.GetItemType() == attachAmbush {
			setCell(cell, attachNone, 0, 0)
		} else {
			cell.ItemFlag = proto.Uint32(cellFlagDisabled)
		}
		group.KillCount = proto.Uint32(group.GetKillCount() + 1)
		session.info.KillCount = proto.Uint32(session.info.GetKillCount() + 1)
		session.info.ContinuousKillCount = proto.Uint32(session.info.GetContinuousKillCount() + 1)
		reply.DropInfo = scenarioDrops(session.template, expedition)
	} else {
		session.info.ContinuousKillCount = proto.Uint32(0)
	}

	// capture: task progress and archive updates (SC_17002) before the
	// result, the new ships (SC_12042) after it
	err = game.tx(ctx, client, func(tx *Tx) error {
		queries := tx.Queries
		tx.newShipsAfterReply = true
		tx.event(events...)
		if techLevel {
			tx.techPointsChanged()
		}
		for _, ship := range updated {
			if err := saveShipBattleState(ctx, queries, ship); err != nil {
				return err
			}
		}
		if win {
			progress, err := chapterProgress(ctx, queries, client.CommanderID, session.template.id)
			if err != nil {
				return err
			}
			if expedition.isBoss() {
				progress.KillBossCount++
			} else {
				progress.KillEnemyCount++
			}
			if err := queries.SaveChapterProgress(ctx, db.SaveChapterProgressParams(progress)); err != nil {
				return err
			}
		}
		if err := addCommanderExp(tx, reply.GetPlayerExp()); err != nil {
			return err
		}
		if win {
			var err error
			if reply.CommanderExp, err = tx.grantBattleMeowfficerExp(session.template, expedition, group, updated); err != nil {
				return err
			}
		}
		if err := tx.grantDrops(reply.DropInfo); err != nil {
			return err
		}
		if err := tx.recordChapterDrops(session.template.id, reply.DropInfo); err != nil {
			return err
		}
		if err := tx.addClassProficiency(proficiency, time.Now()); err != nil {
			return err
		}
		return session.save(ctx, queries, client.CommanderID)
	})
	if err != nil {
		return err
	}
	// capture: the mailbox counts follow the result (mail arrived meanwhile)
	counts, err := mailCounts(ctx, game.DB.Queries, client.CommanderID)
	if err != nil {
		return err
	}
	if err := client.PushAfterReply(counts); err != nil {
		return err
	}
	return client.Send(reply)
}

// feedsClass tells whether a ship's battle exp, once added (addShipExp),
// gives the lecture hall proficiency (FinishStageCommand.addShipsExp with
// accumulate, as BattleGateScenario passes): a ship that can_get_proficency
// above gameset level_get_proficency, or at its max level with exp left.
func feedsClass(ship db.Ship) bool {
	return shipLevelings[ship.TemplateID].accumulate &&
		(ship.Level > levelGetProficency || ship.Level == ship.MaxLevel && ship.Exp > 0)
}

// battleMVP is the ship of the statistics that dealt the most damage (the
// first one on a tie), among byID's.
func battleMVP(stats []*protobuf.STATISTICSINFO, byID map[uint32]db.Ship) uint32 {
	mvp, best := uint32(0), uint32(0)
	for _, stat := range stats {
		if _, ok := byID[stat.GetShipId()]; ok && (mvp == 0 || stat.GetDamageCause() > best) {
			mvp, best = stat.GetShipId(), stat.GetDamageCause()
		}
	}
	return mvp
}

// battleShips is what a battle's ships get (settleBattleShips).
type battleShips struct {
	exps        []*protobuf.SHIP_EXP // SC_40004 ship_exp_list
	updated     []db.Ship            // to save (saveShipBattleState)
	proficiency []uint32             // exps the lecture hall takes a share of
	techLevel   bool                 // a ship reached level 120: pt_level
	events      []TaskEvent          // the exp earned (TaskSubSortieExp)
}

// settleBattleShips gives the ships of a battle's statistics (the ones of
// byID) their exp (FinishStageCommand.addShipsExp: exp of each, added by
// the client with accumulate) and, on a win, affinity (intimacy 10000 +
// affinity gained).
func settleBattleShips(stats []*protobuf.STATISTICSINFO, byID map[uint32]db.Ship, exp func(db.Ship) uint32, win bool, flagship, mvp uint32) battleShips {
	var result battleShips
	for _, stat := range stats {
		ship, ok := byID[stat.GetShipId()]
		if !ok {
			continue
		}
		gained := exp(ship)
		result.events = append(result.events, TaskEvent{SubType: TaskSubSortieExp, Count: gained})
		affinity := uint32(0)
		if win {
			affinity = battleAffinity(ship.ID == flagship, ship.ID == mvp)
		}
		result.exps = append(result.exps, &protobuf.SHIP_EXP{
			ShipId:   proto.Uint32(ship.ID),
			Exp:      proto.Uint32(gained),
			Intimacy: proto.Uint32(maxIntimacy + affinity),
		})
		before := ship.Level
		addShipExp(&ship, gained)
		result.techLevel = result.techLevel || (before < techShipLevel && ship.Level >= techShipLevel)
		if feedsClass(ship) {
			result.proficiency = append(result.proficiency, gained)
		}
		ship.Intimacy = min(ship.Intimacy+affinity, maxIntimacy)
		result.updated = append(result.updated, ship)
	}
	return result
}

// battleAffinity is the affinity (intimacy, 1/100) a win gives. It is random
// server-side; the ranges cover what the capture's two battles gave (4-5 for
// any ship, 4-9 for the flagship, 8-14 for the MVP).
func battleAffinity(flagship, mvp bool) uint32 {
	gain := 4 + rand.Uint32N(2)
	if flagship {
		gain += rand.Uint32N(6)
	}
	if mvp {
		gain += 4 + rand.Uint32N(6)
	}
	return gain
}

func saveShipBattleState(ctx context.Context, queries *db.Queries, ship db.Ship) error {
	return queries.UpdateShipBattleState(ctx, db.UpdateShipBattleStateParams{
		Level:       ship.Level,
		Exp:         ship.Exp,
		Intimacy:    ship.Intimacy,
		Energy:      ship.Energy,
		ID:          ship.ID,
		CommanderID: ship.CommanderID,
	})
}

// scenarioDrops rolls a won battle's loot. Drop tables live server-side; this
// follows the capture: coins for every battle, and for a boss a Specialized
// Core plus draws from the chapter's award pools (tech pack, gear parts, a
// ship), in the capture's order.
//
// A battle with its own award list (the campaign enemies carry one) draws its
// items from that: a normal enemy drops the chapter's designs, gear parts and
// packs, not only coins.
func scenarioDrops(template *chapterTemplate, expedition expeditionTemplate) []*protobuf.DROPINFO {
	items, ships := expedition.pools()
	if len(items) == 0 && len(ships) == 0 {
		// no table of its own (the auto repeat's anonymous enemies): the
		// chapter's pools stand in
		return chapterScenarioDrops(template, expedition)
	}
	logger.WithFields("Drops",
		logger.FieldValue("enemy", expedition.Level),
		logger.FieldValue("boss", expedition.isBoss()),
		logger.FieldValue("items", len(items)),
		logger.FieldValue("ships", len(ships)),
	).Debug("battle drops")
	drops := []*protobuf.DROPINFO{dropInfo(dropTypeResource, ResourceGold, scenarioBossGold)}
	if !expedition.isBoss() {
		drops[0] = dropInfo(dropTypeResource, ResourceGold, scenarioGold)
		return appendDraws(drops, items, battleItemDraws)
	}
	drops = append(drops, dropInfo(dropTypeVItem, vitemSpecializedCore, 1))
	// the boss's own list, then the chapter's ships: an enemy's list names
	// the ship drop through the "unknown character" placeholder, which has
	// no pool of its own
	drops = appendDraws(drops, items, bossItemDraws)
	return appendDraws(drops, chapterShips(template), bossShips)
}

// pools splits an enemy's award_display into what it can drop: the item
// entries (a virtual placeholder like the "unknown design" box is opened on
// receipt, so it stays as it is) and the ships.
func (expedition expeditionTemplate) pools() (items, ships []dropRef) {
	for _, award := range expedition.Awards {
		if len(award) < 2 {
			continue
		}
		kind, id := award[0], award[1]
		switch kind {
		case dropTypeShip:
			ships = append(ships, dropRef{kind, id})
		case dropTypeItem:
			// a virtual id with a pool is a box, a known item is itself;
			// the other virtual ids are placeholders the server fills
			if len(dropPools[id]) > 0 || itemNameTypes[id].Type != 0 {
				items = append(items, dropRef{kind, id})
			}
		}
	}
	return items, ships
}

// chapterShips is a chapter's ship drop table (its awards' ship pools).
func chapterShips(template *chapterTemplate) []dropRef {
	var ships []dropRef
	for _, award := range template.awards {
		for _, ref := range dropPools[award[1]] {
			if ref.kind == dropTypeShip {
				ships = append(ships, ref)
			}
		}
	}
	return ships
}

// chapterScenarioDrops is the fallback for a battle without a list of its own.
func chapterScenarioDrops(template *chapterTemplate, expedition expeditionTemplate) []*protobuf.DROPINFO {
	if !expedition.isBoss() {
		return []*protobuf.DROPINFO{dropInfo(dropTypeResource, ResourceGold, scenarioGold)}
	}
	drops := []*protobuf.DROPINFO{
		dropInfo(dropTypeVItem, vitemSpecializedCore, 1),
		dropInfo(dropTypeResource, ResourceGold, scenarioBossGold),
	}
	var techPacks, parts, ships []dropRef
	for _, award := range template.awards {
		pool := dropPools[award[1]]
		if len(pool) == 0 {
			// not a virtual item: the data lists the item itself, which is
			// how a hard mode chapter gives its core data and equipment
			count := uint32(1)
			if award[1] == itemCoreData {
				// the amount per chapter is the wiki's 核心兑换 table
				count = template.coreData
			}
			if count > 0 {
				drops = mergeDrop(drops, award[0], award[1], count)
			}
			continue
		}
		switch {
		case pool[0].kind == dropTypeShip:
			ships = pool
		case pool[0].id >= 30000:
			techPacks = pool
		default:
			parts = pool
		}
	}
	drops = appendDraws(drops, techPacks, bossTechPacks)
	drops = appendDraws(drops, parts, bossGearParts)
	return appendDraws(drops, ships, bossShips)
}

// appendDraws draws count times from pool, merging repeats (the capture's
// "17041 x2").
func appendDraws(drops []*protobuf.DROPINFO, pool []dropRef, count int) []*protobuf.DROPINFO {
	if len(pool) == 0 {
		return drops
	}
	start := len(drops)
	for range count {
		ref := pool[rand.IntN(len(pool))]
		merged := false
		for _, existing := range drops[start:] {
			if ref.kind != dropTypeShip && existing.GetType() == ref.kind && existing.GetId() == ref.id {
				existing.Number = proto.Uint32(existing.GetNumber() + 1)
				merged = true
			}
		}
		if !merged {
			drops = append(drops, dropInfo(ref.kind, ref.id, 1))
		}
	}
	return drops
}
