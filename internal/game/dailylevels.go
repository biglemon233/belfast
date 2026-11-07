package game

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// const.lua SYSTEM_*: the daily challenges (BattleGateRoutine, and
// BattleGateSubRoutine for the submarine one)
const (
	systemRoutine    = 2
	systemSubRoutine = 11
)

// expedition_daily_template.limit_type: when the attempts come back
// (DailyLevelProxy:resetDailyCount)
const (
	dailyResetDay  = 1
	dailyResetWeek = 2 // on the server's Monday
)

// expedition_daily_template row
type dailyLevelConfig struct {
	// [stage (expedition_data_template id), commander level it needs]
	Stages      [][2]uint32     `json:"expedition_and_lv_limit_list"`
	LimitTime   uint32          `json:"limit_time"` // attempts per period, 0 none
	LimitType   uint32          `json:"limit_type"`
	Weekday     []uint32        `json:"weekday"`      // TimeMgr:GetServerWeek days it is open
	LimitPeriod json.RawMessage `json:"limit_period"` // a time window, or "" for always
}

// dailyLevel is a daily challenge.
type dailyLevel struct {
	id        uint32
	limit     uint32
	limitType uint32
	weekdays  []uint32
	period    *gamedata.Window // nil when it isn't limited in time
}

// dailyStage is a stage of a daily challenge.
type dailyStage struct {
	id     uint32
	level  *dailyLevel
	minLvl uint32 // the commander level it needs
	awards []dailyAward
	// server/daily_awards.json: the units its chance entries give when they
	// are tiers (1 without), and the counts of its items by battle score
	tierCount  uint32
	rankCounts map[uint32]map[uint32]uint32
}

// dailyAward is an award_display entry of a daily stage: count from min to
// max, or, with chance (a percentage), one unit at that chance.
type dailyAward struct {
	kind, id uint32
	min, max uint32
	chance   uint32
}

// dailyLevels are the daily challenges by id, dailyStages their stages by
// stage id.
var (
	dailyLevels map[uint32]*dailyLevel
	dailyStages map[uint32]*dailyStage
)

// expedition_data_template row, for its award_display: [[type, id, count]]
// with count a number or a string ("1~2", "60%chance", "Few"...)
type expeditionAwardsConfig struct {
	AwardDisplay [][]json.RawMessage `json:"award_display"`
}

// loadDailyLevels reads the daily challenges and the drops of their stages.
// The drop tables are server-side; the drops follow the stage's
// award_display (what DailyLevelScene shows): a count or a range ("1~2") is
// given as is, "N%chance" entries one unit at that chance (drawn as one pick
// when they add up to 100%, as the tiers of a stage do, else each on its
// own). Containers and "Mystery" placeholders are drawn like a pack's
// content (resolveContent). Entries whose amount is only a word ("Few",
// "Numerous": the coins of every stage) have no amount in the client data
// and are left out. server/daily_awards.json (loadDailyAwards) adds what
// only the wiki gives: how many tier picks a clear makes, counts by rank.
func loadDailyLevels() error {
	var levels map[uint32]dailyLevelConfig
	var expeditions map[uint32]expeditionAwardsConfig
	err := gamedata.LoadAll(
		gamedata.Load("expedition_daily_template", &levels),
		gamedata.Load("expedition_data_template", &expeditions),
	)
	if err != nil {
		return err
	}
	dailyLevels = make(map[uint32]*dailyLevel, len(levels))
	dailyStages = map[uint32]*dailyStage{}
	vague := 0
	for id, row := range levels {
		level := &dailyLevel{id: id, limit: row.LimitTime, limitType: row.LimitType, weekdays: row.Weekday}
		if len(row.LimitPeriod) > 0 && row.LimitPeriod[0] == '[' {
			var window gamedata.Window
			if err := json.Unmarshal(row.LimitPeriod, &window); err != nil {
				return err
			}
			level.period = &window
		}
		dailyLevels[id] = level
		for _, entry := range row.Stages {
			stage := &dailyStage{id: entry[0], level: level, minLvl: entry[1], tierCount: 1}
			for _, award := range expeditions[entry[0]].AwardDisplay {
				parsed, ok := parseDailyAward(award)
				if !ok {
					vague++
					continue
				}
				stage.awards = append(stage.awards, parsed)
			}
			dailyStages[entry[0]] = stage
		}
	}
	if vague > 0 {
		logger.WithFields("DailyLevel", logger.FieldValue("entries", vague)).Warn("daily stage awards without an amount (coins...) are not given")
	}
	return loadDailyAwards()
}

// loadDailyAwards reads server/daily_awards.json ({"stages": {"<stage>":
// {"tier_count": n, "rank_counts": {"<item>": {"S": n, ...}}}}}), wiki
// numbers the client data doesn't have. A tier count only applies to a
// stage whose chance entries add up to 100, a rank count only to an item of
// the stage's award_display and within its range (the client data wins);
// others are logged and left out. Without the file a clear gives one unit
// of its tiers and the award_display range whatever the rank.
func loadDailyAwards() error {
	raw, err := gamedata.ServerFile("daily_awards.json")
	if err != nil {
		return err
	}
	if raw == nil {
		logger.WithFields("DailyLevel").Warn("no server/daily_awards.json: daily stages give one unit of their tiers")
		return nil
	}
	var file struct {
		Stages map[uint32]struct {
			TierCount  uint32                       `json:"tier_count"`
			RankCounts map[uint32]map[string]uint32 `json:"rank_counts"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("daily_awards.json: %w", err)
	}
	for id, row := range file.Stages {
		stage := dailyStages[id]
		if stage == nil {
			logger.WithFields("DailyLevel", logger.FieldValue("stage", id)).Warn("daily_awards.json: not a daily stage")
			continue
		}
		if row.TierCount > 0 {
			if stage.tierChances() == 100 {
				stage.tierCount = row.TierCount
			} else {
				logger.WithFields("DailyLevel", logger.FieldValue("stage", id)).Warn("daily_awards.json: tier_count for a stage without tiers adding up to 100%")
			}
		}
		for item, ranks := range row.RankCounts {
			i := slices.IndexFunc(stage.awards, func(award dailyAward) bool { return award.id == item && award.chance == 0 })
			for letter, count := range ranks {
				score, ok := scoreLetters[letter]
				if i < 0 || !ok || score <= scoreC || count < stage.awards[i].min || count > stage.awards[i].max {
					logger.WithFields("DailyLevel", logger.FieldValue("stage", id), logger.FieldValue("item", item), logger.FieldValue("rank", letter)).Warn("daily_awards.json: rank count outside the stage's award_display")
					continue
				}
				if stage.rankCounts == nil {
					stage.rankCounts = map[uint32]map[uint32]uint32{}
				}
				if stage.rankCounts[item] == nil {
					stage.rankCounts[item] = map[uint32]uint32{}
				}
				stage.rankCounts[item][score] = count
			}
		}
	}
	return nil
}

// tierChances is what the stage's chance entries add up to.
func (stage *dailyStage) tierChances() uint32 {
	total := uint32(0)
	for _, award := range stage.awards {
		total += award.chance
	}
	return total
}

// parseDailyAward reads an award_display [type, id, count] entry; false when
// its count isn't an amount.
func parseDailyAward(entry []json.RawMessage) (dailyAward, bool) {
	var award dailyAward
	if len(entry) < 3 || json.Unmarshal(entry[0], &award.kind) != nil || json.Unmarshal(entry[1], &award.id) != nil {
		return award, false
	}
	var number uint32
	if json.Unmarshal(entry[2], &number) == nil {
		award.min, award.max = number, number
		return award, true
	}
	var text string
	if json.Unmarshal(entry[2], &text) != nil {
		return award, false
	}
	if percent, ok := strings.CutSuffix(text, "%chance"); ok {
		chance, err := strconv.ParseUint(percent, 10, 32)
		award.chance = uint32(chance)
		return award, err == nil && chance > 0
	}
	// the localised client data writes "20%概率" (CN), so any "%" marks the
	// entry as a chance: take the digits in front of it
	if index := strings.Index(text, "%"); index > 0 {
		chance, err := strconv.ParseUint(text[:index], 10, 32)
		award.chance = uint32(chance)
		return award, err == nil && chance > 0
	}
	low, high, isRange := strings.Cut(text, "~")
	if !isRange {
		high = low
	}
	lowN, errLow := strconv.ParseUint(low, 10, 32)
	highN, errHigh := strconv.ParseUint(high, 10, 32)
	if errLow != nil || errHigh != nil || highN < lowN {
		return award, false
	}
	award.min, award.max = uint32(lowN), uint32(highN)
	return award, true
}

// roll draws the drops of one clear with score (an S for a quick battle),
// keeping what grantDrops can store: tiers adding up to 100% are picked
// tierCount times, an item with a count for the rank gets it.
func (stage *dailyStage) roll(score uint32) []*protobuf.DROPINFO {
	var drops []*protobuf.DROPINFO
	var chances []dailyAward
	total := uint32(0)
	for _, award := range stage.awards {
		if award.chance > 0 {
			chances = append(chances, award)
			total += award.chance
			continue
		}
		count := award.min + rand.Uint32N(award.max-award.min+1)
		if ranked, ok := stage.rankCounts[award.id][score]; ok {
			count = ranked
		}
		if count > 0 {
			drops = resolveContent(drops, award.kind, award.id, count)
		}
	}
	if total == 100 {
		for range stage.tierCount {
			if i := weightedPick(rand.Uint32N, chances, func(award dailyAward) uint32 { return award.chance }); i >= 0 {
				drops = resolveContent(drops, chances[i].kind, chances[i].id, 1)
			}
		}
	} else {
		for _, award := range chances {
			if rand.Uint32N(100) < award.chance {
				drops = resolveContent(drops, award.kind, award.id, 1)
			}
		}
	}
	return slices.DeleteFunc(drops, func(drop *protobuf.DROPINFO) bool { return !storableDrop(drop.GetType(), drop.GetId()) })
}

// periodStart is when the running attempt period of the challenge began
// (unix seconds), 0 for one whose attempts never come back.
func (level *dailyLevel) periodStart(now time.Time) int64 {
	switch level.limitType {
	case dailyResetDay:
		return startOfDay(now).Unix()
	case dailyResetWeek:
		return periodStart(now, week).Unix()
	}
	return 0
}

// open is whether the challenge can be played now: in its time window, on
// one of its days (DailyLevelScene locks it otherwise).
func (level *dailyLevel) open(now time.Time) bool {
	if level.period != nil && !level.period.Open(now) {
		return false
	}
	return len(level.weekdays) == 0 || slices.Contains(level.weekdays, serverWeekday(now))
}

// dailyCounts are the attempts spent on each challenge in its running
// period.
func dailyCounts(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (map[uint32]uint32, error) {
	rows, err := queries.ListDailyLevelCounts(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	counts := make(map[uint32]uint32, len(rows))
	for _, row := range rows {
		level := dailyLevels[row.DailyID]
		if level == nil || row.Period != level.periodStart(now) {
			continue
		}
		counts[row.DailyID] = row.Count
	}
	return counts, nil
}

// dailyLevelState is the login's SC_13201 (DailyLevelProxy): the attempts
// spent this period and the stages that can be quick battled. Hard mode and
// escort counts aren't kept.
func dailyLevelState(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (*protobuf.SC_13201, error) {
	counts, err := dailyCounts(ctx, queries, commanderID, now)
	if err != nil {
		return nil, err
	}
	msg := &protobuf.SC_13201{}
	for id, count := range counts {
		msg.CountList = append(msg.CountList, &protobuf.EXPEDITION_DAILY_COUNT{Id: proto.Uint32(id), Count: proto.Uint32(count)})
	}
	slices.SortFunc(msg.CountList, func(a, b *protobuf.EXPEDITION_DAILY_COUNT) int { return int(a.GetId()) - int(b.GetId()) })
	if msg.QuickExpeditionList, err = queries.ListDailyQuickStages(ctx, commanderID); err != nil {
		return nil, err
	}
	return msg, nil
}

// dailyAttemptsUsed is the attempts spent on stage's challenge this
// period, refusing a stage that can't be played now (closed, or above the
// commander's level: DailyLevelScene's locks).
func dailyAttemptsUsed(ctx context.Context, queries *db.Queries, commanderID uint32, stage *dailyStage, now time.Time) (uint32, error) {
	commander, err := queries.GetCommander(ctx, commanderID)
	if err != nil {
		return 0, err
	}
	if !stage.level.open(now) || commander.Level < stage.minLvl {
		return 0, errRefused
	}
	counts, err := dailyCounts(ctx, queries, commanderID, now)
	return counts[stage.level.id], err
}

// saveDailyAttempts sets the attempts spent on a challenge this period.
func saveDailyAttempts(ctx context.Context, queries *db.Queries, commanderID uint32, level *dailyLevel, count uint32, now time.Time) error {
	return queries.SaveDailyLevelCount(ctx, db.SaveDailyLevelCountParams{
		CommanderID: commanderID,
		DailyID:     level.id,
		Count:       count,
		Period:      level.periodStart(now),
	})
}

// checkDailyBegin is whether a daily challenge battle can start (CS_40001,
// data the stage): playable, with an attempt left. Nothing is spent until
// the battle is won.
func checkDailyBegin(ctx context.Context, queries *db.Queries, commanderID, stageID uint32, now time.Time) error {
	stage := dailyStages[stageID]
	if stage == nil {
		return errRefused
	}
	used, err := dailyAttemptsUsed(ctx, queries, commanderID, stage, now)
	if err != nil {
		return err
	}
	if used >= stage.level.limit {
		return errRefused
	}
	return nil
}

// const.lua ShipType.SubShipType (QianTing, QianMu, FengFanS): ships that
// cost no oil to sortie (Ship:getStartBattleExpend)
var subShipTypes = []uint32{8, 17, 22}

// battleStartOil is Fleet:getStartCost, the oil the client spends when a
// battle starts: each ship's oil_at_start, none for a submarine.
func battleStartOil(ships map[uint32]db.Ship) uint32 {
	oil := uint32(0)
	for _, ship := range ships {
		template := ownableShips[ship.TemplateID]
		if !slices.Contains(subShipTypes, template.Type) {
			oil += template.OilAtStart
		}
	}
	return oil
}

// battleEndOil is Fleet:getEndCost, the oil the client spends on a won
// battle (GeneralPlayerCosume): each ship's oil_at_end x its level's
// ship_level fight_oil_ratio / 10000, floored (Ship:getEndBattleExpend; a
// level past the table takes its last row, getConfigFromLevel1).
func battleEndOil(ships map[uint32]db.Ship) uint32 {
	oil := uint32(0)
	for _, ship := range ships {
		if len(shipFightOilRatio) == 0 || ship.Level == 0 {
			continue
		}
		ratio := shipFightOilRatio[min(int(ship.Level), len(shipFightOilRatio))-1]
		oil += ownableShips[ship.TemplateID].OilAtEnd * ratio / 10000
	}
	return oil
}

// finishDaily settles a daily challenge battle (BattleGateRoutine.Exit,
// BattleGateSubRoutine.Exit; battle_cost_template rows 2 and 11 award ship
// exp and cost oil). As the client does on the reply, a win (score above C)
// spends an attempt and the fleet's end-of-battle oil (battleEndOil, the
// start oil went at CS_40001), and an S rank makes the stage quick battle
// ready (DailyLevelProxy:AddQuickStage). A win drops the stage's awards
// (dailyStage.roll), counts for the daily challenge and enemy sunk tasks,
// and gives the ships expedition exp x rank x morale (x MVP / flagship) and
// affinity, as a campaign battle without its x2 (scenarioExpRate): the
// wiki's S-rank EXP of every daily stage it lists is the stage's exp x 1.2.
// A loss gives nothing (the wiki: no exp). Commander exp is not given (no
// table, capture or wiki number for a daily battle), nor meowfficer exp
// (the client's Exit never reads commander_exp).
func (game *Game) finishDaily(client *connection.Client, req *protobuf.CS_40003) error {
	ctx, now := context.Background(), time.Now()
	stage := dailyStages[req.GetData()]
	expedition, known := expeditionTemplates[req.GetData()]
	if stage == nil || !known {
		return client.Send(&protobuf.SC_40004{Result: result(errRefused)})
	}
	score := min(req.GetScore(), scoreS)
	reply := &protobuf.SC_40004{Result: result(nil)}
	if score <= scoreC {
		return client.Send(reply)
	}
	stats := req.GetStatistics()
	statShips := make([]uint32, len(stats))
	for i, stat := range stats {
		statShips[i] = stat.GetShipId()
	}
	byID, err := shipsByID(ctx, game.DB.Queries, client.CommanderID, statShips)
	if err != nil {
		return err
	}
	endOil := battleEndOil(byID)
	mvp := battleMVP(stats, byID)
	reply.Mvp = proto.Uint32(mvp)
	// the statistics follow BayProxy:getSortShipsByFleet, the main fleet's
	// flagship first; a submarine fleet has none
	flagship := uint32(0)
	if req.GetSystem() == systemRoutine && len(stats) > 0 {
		flagship = stats[0].GetShipId()
	}
	ships := settleBattleShips(stats, byID, func(ship db.Ship) uint32 {
		return shipBattleExp(expedition, 1, score, ship, ship.ID == flagship, ship.ID == mvp)
	}, true, flagship, mvp)
	reply.ShipExpList = ships.exps
	err = game.tx(ctx, client, func(tx *Tx) error {
		tx.event(ships.events...)
		tx.event(TaskEvent{SubType: TaskSubDailyStage, Target: stage.id, Count: 1})
		if kills := uint32(len(req.GetKillIdList())); kills > 0 {
			tx.event(TaskEvent{SubType: TaskSubDefeatEnemy, Count: kills})
		}
		if ships.techLevel {
			tx.techPointsChanged()
		}
		for _, ship := range ships.updated {
			if err := saveShipBattleState(ctx, tx.Queries, ship); err != nil {
				return err
			}
		}
		if err := tx.addClassProficiency(ships.proficiency, now); err != nil {
			return err
		}
		// the client checked the start and end oil before the battle
		if err := tx.SpendResource(ResourceOil, endOil); err == ErrNotEnough {
			logger.WithFields("DailyLevel", logger.FieldValue("oil", endOil)).Warn("not enough oil left for the end of a daily battle")
		} else if err != nil {
			return err
		}
		// the battle started with an attempt left: count the win even if
		// the period turned meanwhile
		counts, err := dailyCounts(ctx, tx.Queries, client.CommanderID, now)
		if err != nil {
			return err
		}
		if err := saveDailyAttempts(ctx, tx.Queries, client.CommanderID, stage.level, counts[stage.level.id]+1, now); err != nil {
			return err
		}
		if score == scoreS {
			err := tx.AddDailyQuickStage(ctx, db.AddDailyQuickStageParams{CommanderID: client.CommanderID, StageID: stage.id})
			if err != nil {
				return err
			}
		}
		reply.DropInfo = stage.roll(score)
		// the commander manual counts a cleared daily challenge
		tx.event(TaskEvent{SubType: TaskSubDailyClear, Count: 1})
		return tx.grantDrops(reply.DropInfo)
	})
	if err != nil {
		return err
	}
	return client.Send(reply)
}

// handleQuitBattle answers QuitBattleCommand (controller/command/stage/
// quitbattlecommand.lua, CS_40005): the client giving up a battle it
// started. Nothing is held between CS_40001 and CS_40003 (attempts and
// rewards are only settled by a finish), so there is nothing to undo; the
// client only reads result (a tip on non-zero). Built from the Lua alone:
// the command isn't registered in the current client (GAME.QUIT_BATTLE only
// stops the battle scene), so it was never captured.
//
//belfast:static
func (game *Game) handleQuitBattle(client *connection.Client, req *protobuf.CS_40005) error {
	return client.Send(&protobuf.SC_40006{Result: result(nil)})
}

// handleDailyQuickBattle answers DailiyQuickBattleCommand (controller/
// command/stage/dailiyquickbattlecommand.lua, CS_40007): cnt attempts of a
// daily challenge stage cleared with an S rank are settled at once, without
// a fight. Each attempt is one QUICK_REWARD of the stage's drops
// (dailyStage.roll; the client adds them through PlayerConst.addTranDrop and
// counts the attempts itself). As the client's tip says
// (dailyLevel_quickfinish), no exp, morale or affinity, and the client
// spends no oil. Refused (result 1) for an unknown or not S-cleared stage, a
// closed challenge, a level too low or too few attempts left. Built from the
// Lua alone (never captured).
func (game *Game) handleDailyQuickBattle(client *connection.Client, req *protobuf.CS_40007) error {
	ctx, now := context.Background(), time.Now()
	stage := dailyStages[req.GetId()]
	if req.GetSystem() != systemRoutine || stage == nil || req.GetCnt() == 0 {
		return client.Send(&protobuf.SC_40008{Result: result(errRefused)})
	}
	reply := &protobuf.SC_40008{Result: result(nil)}
	err := game.tx(ctx, client, func(tx *Tx) error {
		quick, err := tx.ListDailyQuickStages(ctx, client.CommanderID)
		if err != nil {
			return err
		}
		if !slices.Contains(quick, req.GetId()) {
			return errRefused
		}
		used, err := dailyAttemptsUsed(ctx, tx.Queries, client.CommanderID, stage, now)
		if err != nil {
			return err
		}
		if used+req.GetCnt() > stage.level.limit {
			return errRefused
		}
		if err := saveDailyAttempts(ctx, tx.Queries, client.CommanderID, stage.level, used+req.GetCnt(), now); err != nil {
			return err
		}
		for range req.GetCnt() {
			drops := stage.roll(scoreS)
			reply.RewardList = append(reply.RewardList, &protobuf.QUICK_REWARD{DropList: drops})
			if err := tx.grantDrops(drops); err != nil {
				return err
			}
		}
		// a quick battle clears the challenge like a fought one
		tx.event(TaskEvent{SubType: TaskSubDailyClear, Count: req.GetCnt()})
		return nil
	})
	if refused(err) {
		return client.Send(&protobuf.SC_40008{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}
