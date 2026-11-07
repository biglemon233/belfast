package game

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ggmolly/belfast/internal/db"
)

// The back-office's timers, progress and research (internal/admin
// area_progress.go). Every write kicks the commander first (the client keeps
// its own copy of what changes) and runs in one transaction (AdminWrite).
// A timer finished from here ends now, as if it had run out: the commander
// claims what it gives in the game, nothing is granted here.

// progressEdit is AdminWrite with the time the edit is made at.
func (game *Game) progressEdit(ctx context.Context, id uint32, fn func(tx *Tx, now time.Time) error) (bool, error) {
	now := time.Now()
	return game.AdminWrite(ctx, id, func(tx *Tx) error { return fn(tx, now) })
}

// Timer kinds FinishTimer ends.
const (
	TimerBuild      = "build"      // key: the build's id
	TimerCommission = "commission" // key: the collection_template id
	TimerLesson     = "lesson"     // key: the Tactical Class room
	TimerField      = "field"      // key: FieldOil, FieldGold or FieldClass
	TimerAuto       = "auto"       // the running Operational Handover
	TimerResearch   = "research"   // key: the offered project's position
	TimerQueue      = "queue"      // key: the research queue entry's id
	TimerBlueprint  = "blueprint"  // the research ships' cold time
	TimerFleetTech  = "fleet_tech" // key: the fleet_tech_group id
	TimerCooldown   = "cooldown"   // key: the Player cdList key
	TimerGuild      = "guild"      // the guild re-join wait
)

// The academy fields, by FieldTable's and FinishTimer's key.
const (
	FieldOil   = 1
	FieldGold  = 2
	FieldClass = 3
)

var fieldEffects = map[uint32]string{FieldOil: effectOilFieldLevel, FieldGold: effectGoldFieldLevel, FieldClass: effectClassFieldLevel}

// FieldTable is an academy field's max level and how long the upgrade from
// level takes (seconds, 0 at the max level).
func FieldTable(field, level uint32) (maxLevel uint32, upgrade int64) {
	table, ok := fieldUpgrades[fieldEffects[field]]
	if !ok {
		return 0, 0
	}
	if level < table.max {
		upgrade = table.rows[level].Time
	}
	return table.max, upgrade
}

// FinishTimer ends one of the commander's running timers now.
func (game *Game) FinishTimer(ctx context.Context, id uint32, kind string, key uint32) (bool, error) {
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		finished, err := tx.finishTimer(kind, key, now)
		if err == nil && !finished {
			err = adminRefused("that timer isn't running")
		}
		return err
	})
}

// FinishAllTimers ends every running timer of the commander now, and tells
// how many it ended, and whether the commander was kicked.
func (game *Game) FinishAllTimers(ctx context.Context, id uint32) (int, bool, error) {
	var count int
	kicked, err := game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		count = 0
		timers, err := tx.runningTimers(now)
		if err != nil {
			return err
		}
		for _, timer := range timers {
			finished, err := tx.finishTimer(timer.kind, timer.key, now)
			if err != nil {
				return err
			}
			if finished {
				count++
			}
		}
		return nil
	})
	return count, kicked, err
}

type runningTimer struct {
	kind string
	key  uint32
}

// runningTimers lists the timers of the commander still running at now.
func (tx *Tx) runningTimers(now time.Time) ([]runningTimer, error) {
	unix := now.Unix()
	var timers []runningTimer
	builds, err := tx.ListBuilds(tx.ctx, tx.commanderID)
	if err != nil {
		return nil, err
	}
	for _, build := range builds {
		if build.FinishTime > unix {
			timers = append(timers, runningTimer{TimerBuild, build.ID})
		}
	}
	commissions, err := tx.ListCommissions(tx.ctx, tx.commanderID)
	if err != nil {
		return nil, err
	}
	for _, row := range commissions {
		if int64(row.FinishTime) > unix {
			timers = append(timers, runningTimer{TimerCommission, row.CollectionID})
		}
	}
	lessons, err := tx.ListSkillClasses(tx.ctx, tx.commanderID)
	if err != nil {
		return nil, err
	}
	for _, lesson := range lessons {
		if lesson.FinishTime > unix {
			timers = append(timers, runningTimer{TimerLesson, lesson.RoomID})
		}
	}
	for _, field := range []uint32{FieldOil, FieldGold, FieldClass} {
		timers = append(timers, runningTimer{TimerField, field})
	}
	timers = append(timers, runningTimer{TimerAuto, 0})
	offers, err := tx.ListTechnologyOffers(tx.ctx, tx.commanderID)
	if err != nil {
		return nil, err
	}
	for _, offer := range offers {
		timers = append(timers, runningTimer{TimerResearch, offer.Pos})
	}
	queue, err := tx.ListTechnologyQueue(tx.ctx, tx.commanderID)
	if err != nil {
		return nil, err
	}
	for _, entry := range queue {
		timers = append(timers, runningTimer{TimerQueue, entry.ID})
	}
	timers = append(timers, runningTimer{TimerBlueprint, 0})
	techs, err := tx.ListFleetTechs(tx.ctx, tx.commanderID)
	if err != nil {
		return nil, err
	}
	for _, tech := range techs {
		timers = append(timers, runningTimer{TimerFleetTech, tech.GroupID})
	}
	cooldowns, err := tx.ListCooldowns(tx.ctx, tx.commanderID)
	if err != nil {
		return nil, err
	}
	for _, cooldown := range cooldowns {
		timers = append(timers, runningTimer{TimerCooldown, cooldown.Key})
	}
	return append(timers, runningTimer{TimerGuild, 0}), nil
}

// finishTimer ends a timer now, as the game ends it once its time is up,
// and tells whether it was running.
func (tx *Tx) finishTimer(kind string, key uint32, now time.Time) (bool, error) {
	unix := now.Unix()
	switch kind {
	case TimerBuild:
		// as a Quick Finisher does (handleBuildQuickFinish): the builds
		// queued behind start now
		builds, err := tx.ListBuilds(tx.ctx, tx.commanderID)
		if err != nil {
			return false, err
		}
		for i, build := range builds {
			if build.ID != key {
				continue
			}
			if build.FinishTime <= unix {
				return false, nil
			}
			builds[i].FinishTime = unix
			return true, rescheduleBuilds(tx.ctx, tx.Queries, builds, unix)
		}
		return false, nil
	case TimerCommission:
		row, err := tx.GetCommission(tx.ctx, db.GetCommissionParams{CommanderID: tx.commanderID, CollectionID: key})
		if errors.Is(err, sql.ErrNoRows) || (err == nil && int64(row.FinishTime) <= unix) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		return true, tx.SetCommissionFinishTime(tx.ctx, db.SetCommissionFinishTimeParams{FinishTime: uint32(unix), CommanderID: tx.commanderID, CollectionID: key})
	case TimerLesson:
		// endLesson then gives the whole lesson's exp: it ran its length
		lesson, err := tx.GetSkillClass(tx.ctx, db.GetSkillClassParams{CommanderID: tx.commanderID, RoomID: key})
		if errors.Is(err, sql.ErrNoRows) || (err == nil && lesson.FinishTime <= unix) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		return true, tx.ProgressSetLessonFinish(tx.ctx, db.ProgressSetLessonFinishParams{FinishTime: unix, CommanderID: tx.commanderID, RoomID: key})
	case TimerField:
		// the login's settleResourceFields levels the field up, from the end
		// of its upgrade on
		effect, ok := fieldEffects[key]
		if !ok {
			return false, nil
		}
		commander, err := tx.GetCommander(tx.ctx, tx.commanderID)
		if err != nil {
			return false, err
		}
		level, upgradeAt := fieldLevel(commander, effect)
		if upgradeAt <= unix {
			return false, nil
		}
		return true, tx.setFieldLevel(effect, level, unix)
	case TimerAuto:
		battles, err := tx.ListChapterAutoBattles(tx.ctx, tx.commanderID)
		if err != nil {
			return false, err
		}
		finished := false
		for _, battle := range battles {
			if battle.FinishAt <= unix {
				continue
			}
			finished = true
			if err := tx.ProgressSetAutoBattleFinish(tx.ctx, db.ProgressSetAutoBattleFinishParams{FinishAt: unix, CommanderID: tx.commanderID, Seq: battle.Seq}); err != nil {
				return false, err
			}
		}
		return finished, nil
	case TimerResearch:
		// its condition task, if any, still has to be done to claim it
		offers, err := tx.ListTechnologyOffers(tx.ctx, tx.commanderID)
		if err != nil {
			return false, err
		}
		for _, offer := range offers {
			if offer.Pos == key && offer.FinishAt > unix {
				return true, tx.SetTechnologyOfferFinish(tx.ctx, db.SetTechnologyOfferFinishParams{FinishAt: unix, CommanderID: tx.commanderID, Pos: offer.Pos})
			}
		}
		return false, nil
	case TimerQueue:
		queue, err := tx.ListTechnologyQueue(tx.ctx, tx.commanderID)
		if err != nil {
			return false, err
		}
		for _, entry := range queue {
			if entry.ID == key && entry.FinishAt > unix {
				return true, tx.ProgressSetResearchQueueFinish(tx.ctx, db.ProgressSetResearchQueueFinishParams{FinishAt: unix, ID: entry.ID, CommanderID: tx.commanderID})
			}
		}
		return false, nil
	case TimerBlueprint:
		state, err := blueprintState(tx.ctx, tx.Queries, tx.commanderID, now)
		if err != nil || state.ColdTime <= unix {
			return false, err
		}
		state.ColdTime = unix
		return true, tx.saveBlueprintState(state)
	case TimerFleetTech:
		// the client finishes the level itself once its time is up (CS_64003)
		tech, err := tx.fleetTech(key)
		if err != nil || tech.StudyTechID == 0 || tech.StudyFinishAt <= unix {
			return false, err
		}
		tech.StudyFinishAt = unix
		return true, tx.saveFleetTech(tech)
	case TimerCooldown:
		until, err := tx.GetCooldown(tx.ctx, db.GetCooldownParams{CommanderID: tx.commanderID, Key: key})
		if errors.Is(err, sql.ErrNoRows) || (err == nil && until <= unix) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		return true, tx.SetCooldown(tx.ctx, db.SetCooldownParams{CommanderID: tx.commanderID, Key: key, Until: unix})
	case TimerGuild:
		until, err := tx.GetGuildWaitTime(tx.ctx, tx.commanderID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && int64(until) <= unix) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		return true, tx.SetGuildWaitTime(tx.ctx, db.SetGuildWaitTimeParams{CommanderID: tx.commanderID, Until: uint32(unix)})
	}
	return false, adminRefused("unknown timer %s", kind)
}

// Counters ResetCounter sets back to zero.
const (
	CounterCommissionDraws = "commission_draws" // today's daily-slot commissions drawn
	CounterQuickFinishes   = "quick_finishes"   // today's Tactical Class quick finishes
)

// ResetCounter sets one of the commander's daily counters back to zero.
func (game *Game) ResetCounter(ctx context.Context, id uint32, counter string) (bool, error) {
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		switch counter {
		case CounterCommissionDraws:
			return tx.ProgressResetCommissionDraws(tx.ctx, tx.commanderID)
		case CounterQuickFinishes:
			return tx.ProgressResetQuickFinishes(tx.ctx, tx.commanderID)
		}
		return adminRefused("unknown counter %s", counter)
	})
}

// CommissionDraws is how many daily-slot commissions a day draws first
// (Daily Resource Extraction ones).
const CommissionDraws = commissionDailyPerDay

// QuickFinishes are the Tactical Class lessons finished instantly today and
// how many the running buffs allow. The commander's own activities count too
// (newUserActivities), the same set handleQuickFinishLesson accepts.
func (game *Game) QuickFinishes(ctx context.Context, commander db.Commander, now time.Time) (used, allowed uint32, err error) {
	allowed, err = quickFinishesAllowed(ctx, game.DB.Queries, commander.ID, now)
	if err != nil {
		return 0, 0, err
	}
	return quickFinishesUsed(commander, now), allowed, nil
}

// BuildPointsMax is the most build points a commander holds (the UR
// exchange's request, BuildShipProxy:changeRegularExchangeCount).
func BuildPointsMax() uint32 { return regularBuildExchange.Request }

// BuildSlots is how many builds can run at once (MAX_BUILD_WORK_COUNT).
func BuildSlots() uint32 { return maxBuilds }

// SetBuildPoints sets the commander's build points, at most BuildPointsMax.
func (game *Game) SetBuildPoints(ctx context.Context, id, points uint32) (bool, error) {
	if points > BuildPointsMax() {
		return false, adminRefused("build points go up to %d", BuildPointsMax())
	}
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		return tx.SetBuildPoints(tx.ctx, db.SetBuildPointsParams{CommanderID: tx.commanderID, ExchangeCount: points})
	})
}

// SetFieldLevel sets an academy field's level, between 1 and its table's
// max, while no upgrade of it runs. The fields are settled at the old
// levels first, so what they produced up to now stays.
func (game *Game) SetFieldLevel(ctx context.Context, id, field, level uint32) (bool, error) {
	effect, ok := fieldEffects[field]
	if !ok {
		return false, adminRefused("unknown field")
	}
	if top := fieldUpgrades[effect].max; level < 1 || level > top {
		return false, adminRefused("the level goes from 1 to %d", top)
	}
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		commander, err := tx.GetCommander(tx.ctx, tx.commanderID)
		if err != nil {
			return err
		}
		if _, upgradeAt := fieldLevel(commander, effect); upgradeAt > now.Unix() {
			return adminRefused("an upgrade of that field is running")
		}
		if err := tx.settleResourceFields(commander, now); err != nil {
			return err
		}
		return tx.setFieldLevel(effect, level, 0)
	})
}

// AutoTime is the commander's Operational Handover time today: the seconds
// used and the day's allowance (the gameset's limit plus what permits
// added).
func AutoTime(ctx context.Context, queries *db.Queries, id uint32, now time.Time) (used, limit int64, err error) {
	state, err := chapterAutoState(ctx, queries, id, now)
	if err != nil {
		return 0, 0, err
	}
	return int64(state.TimeUsed), int64(chapterAutoTimeLimit) + int64(state.ExtraTime), nil
}

// ChapterSet is a stage's progress as SetChapters stores it: progress 100
// once cleared (never lowered), the counts its stars read (kill_boss_count, kill_enemy_count,
// take_box_count), nil to keep them.
type ChapterSet struct {
	ID       uint32
	Progress uint32
	Counts   *[3]uint32
	// Wins is defeat_count, the times it was won (today's no more than it)
	Wins *uint32
}

// SetChapters stores stages' progress. A stage it clears counts for the
// tasks as a cleared stage does (TaskSubClearStage); the star and clear
// rewards are claimed in the game.
func (game *Game) SetChapters(ctx context.Context, id uint32, sets []ChapterSet) (bool, error) {
	for _, set := range sets {
		if _, ok := chapterTemplates[set.ID]; !ok || set.Progress > 100 {
			return false, adminRefused("no stage %d", set.ID)
		}
	}
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		for _, set := range sets {
			progress, err := chapterProgress(tx.ctx, tx.Queries, tx.commanderID, set.ID)
			if err != nil {
				return err
			}
			if progress.Progress < 100 && set.Progress >= 100 {
				tx.event(TaskEvent{SubType: TaskSubClearStage, Target: set.ID, Count: 1})
			}
			progress.Progress = max(progress.Progress, set.Progress)
			if set.Counts != nil {
				progress.KillBossCount, progress.KillEnemyCount, progress.TakeBoxCount = set.Counts[0], set.Counts[1], set.Counts[2]
			}
			if set.Wins != nil {
				progress.DefeatCount = *set.Wins
				progress.TodayDefeatCount = min(progress.TodayDefeatCount, *set.Wins)
			}
			if err := tx.SaveChapterProgress(tx.ctx, db.SaveChapterProgressParams(progress)); err != nil {
				return err
			}
		}
		return nil
	})
}

// EndSortie ends the commander's sortie as a retreat before winning does.
func (game *Game) EndSortie(ctx context.Context, id uint32) (bool, error) {
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		if _, err := tx.GetChapterState(tx.ctx, tx.commanderID); errors.Is(err, sql.ErrNoRows) {
			return adminRefused("no sortie running")
		} else if err != nil {
			return err
		}
		return tx.DeleteChapterState(tx.ctx, tx.commanderID)
	})
}

// Mission kinds SetMissionProgress sets.
const (
	MissionTask     = "task"     // commander_tasks
	MissionWeekly   = "weekly"   // commander_weekly_tasks
	MissionActivity = "activity" // commander_activity_tasks of act
)

// SetMissionProgress sets an unclaimed mission's progress, at most its
// target_num.
func (game *Game) SetMissionProgress(ctx context.Context, id uint32, kind string, act, task, progress uint32) (bool, error) {
	var target uint32
	switch kind {
	case MissionTask, MissionActivity:
		config, ok := taskConfigs[task]
		if !ok {
			return false, adminRefused("no task %d", task)
		}
		target = config.TargetNum
	case MissionWeekly:
		config, ok := weeklyTaskConfigs[task]
		if !ok {
			return false, adminRefused("no weekly mission %d", task)
		}
		target = config.TargetNum
	default:
		return false, adminRefused("unknown mission kind %s", kind)
	}
	if progress > target {
		return false, adminRefused("the target is %d", target)
	}
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		switch kind {
		case MissionTask:
			row, err := tx.GetTask(tx.ctx, db.GetTaskParams{CommanderID: tx.commanderID, TaskID: task})
			if errors.Is(err, sql.ErrNoRows) || (err == nil && row.SubmitTime != 0) {
				return adminRefused("the commander has no task %d running", task)
			} else if err != nil {
				return err
			}
			return tx.SetTaskProgress(tx.ctx, db.SetTaskProgressParams{Progress: progress, CommanderID: tx.commanderID, TaskID: task})
		case MissionWeekly:
			row, err := tx.GetWeeklyTask(tx.ctx, db.GetWeeklyTaskParams{CommanderID: tx.commanderID, TaskID: task})
			if errors.Is(err, sql.ErrNoRows) || (err == nil && row.Claimed) {
				return adminRefused("the commander has no weekly mission %d running", task)
			} else if err != nil {
				return err
			}
			return tx.SetWeeklyTaskProgress(tx.ctx, db.SetWeeklyTaskProgressParams{Progress: progress, CommanderID: tx.commanderID, TaskID: task})
		}
		rows, err := tx.ListActivityTasks(tx.ctx, tx.commanderID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.ActID == act && row.TaskID == task && !row.Finished {
				return tx.SetActivityTaskProgress(tx.ctx, db.SetActivityTaskProgressParams{CommanderID: tx.commanderID, ActID: act, TaskID: task, Progress: progress})
			}
		}
		return adminRefused("the commander has no such activity task")
	})
}

// SetGuide sets the commander's tutorial progress: the old (SG001) and new
// (SG002) guides' step indexes.
func (game *Game) SetGuide(ctx context.Context, id, guide, newGuide uint32) (bool, error) {
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		if err := tx.SetGuideIndex(tx.ctx, db.SetGuideIndexParams{GuideIndex: guide, ID: tx.commanderID}); err != nil {
			return err
		}
		return tx.SetNewGuideIndex(tx.ctx, db.SetNewGuideIndexParams{NewGuideIndex: newGuide, ID: tx.commanderID})
	})
}

// BlueprintDev is a research ship's development: its level (fate
// simulation phases past the max level), exp, and what its table allows.
type BlueprintDev struct {
	Level    uint32 `json:"level"`
	MaxLevel uint32 `json:"max_level"`
	Fate     int    `json:"fate"` // -1 before the max level
	MaxFate  int    `json:"max_fate"`
	Exp      uint32 `json:"exp"`
	NeedExp  uint32 `json:"need_exp"` // to the next level or phase, 0 when maxed
	// ItemExp is the exp one of the ship's blueprints gives
	ItemExp uint32 `json:"item_exp"`
}

// BlueprintProgress is a built research ship's development.
func BlueprintProgress(ctx context.Context, queries *db.Queries, blueprintID, shipID uint32) (BlueprintDev, bool, error) {
	config, ok := blueprintConfigs[blueprintID]
	if !ok || shipID == 0 {
		return BlueprintDev{}, false, nil
	}
	progress, err := shipBlueprintProgress(ctx, queries, config, shipID)
	if err != nil {
		return BlueprintDev{}, false, err
	}
	dev := BlueprintDev{
		Level: progress.level, MaxLevel: config.maxLevel(), Fate: progress.fate, MaxFate: config.maxFate(),
		Exp: progress.exp, ItemExp: config.itemExp(),
	}
	switch {
	case progress.level < config.maxLevel():
		dev.NeedExp = config.levels[progress.level].NeedExp
	case progress.fate >= 0 && progress.fate < config.maxFate():
		dev.NeedExp = config.fates[progress.fate].NeedExp
	}
	return dev, true, nil
}

// DevelopBlueprint develops a built research ship up to level (fate
// simulation phases past the max level counted on), as using that many of
// its blueprints does (developBlueprint, its skills, skin and limit breaks
// included) without spending them. Development only goes up, and as far as
// the ship's level allows.
// UnlockBlueprint builds a research ship at once, as FinishBluePrintCommand
// does once every task of its research is claimed, but without its opening
// tasks, its research tasks or the cold time. A ship of its group the
// commander already owns (a granted one) becomes the research's ship
// instead of a second one being built.
func (game *Game) UnlockBlueprint(ctx context.Context, id, blueprintID uint32) (bool, error) {
	config, ok := blueprintConfigs[blueprintID]
	if !ok {
		return false, adminRefused("no research ship %d", blueprintID)
	}
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		row, err := blueprintRow(tx.ctx, tx.Queries, tx.commanderID, blueprintID)
		if err != nil {
			return err
		}
		if row.ShipID != 0 {
			return adminRefused("the ship is built already")
		}
		ships, err := tx.ListShipsByCommander(tx.ctx, tx.commanderID)
		if err != nil {
			return err
		}
		for _, ship := range ships {
			if ship.TemplateID/10 == blueprintID {
				row.ShipID, row.StartTime, row.Duration = ship.ID, 0, 0
				return tx.saveBlueprint(row)
			}
		}
		_, err = tx.buildBlueprintShip(config, row)
		return err
	})
}

func (game *Game) DevelopBlueprint(ctx context.Context, id, blueprintID, level uint32) (bool, error) {
	config, ok := blueprintConfigs[blueprintID]
	if !ok || config.itemExp() == 0 {
		return false, adminRefused("no research ship %d", blueprintID)
	}
	top := config.maxLevel() + uint32(config.maxFate())
	if level > top {
		return false, adminRefused("the development goes up to %d", top)
	}
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		row, err := blueprintRow(tx.ctx, tx.Queries, tx.commanderID, blueprintID)
		if err != nil {
			return err
		}
		if row.ShipID == 0 {
			return adminRefused("the ship isn't built yet")
		}
		before, err := shipBlueprintProgress(tx.ctx, tx.Queries, config, row.ShipID)
		if err != nil {
			return err
		}
		if level <= before.stored() {
			return adminRefused("the development only goes up")
		}
		// the fewest blueprints reaching level
		reach := func(count uint32) bool { return before.addExp(config, count*config.itemExp()).stored() >= level }
		low, high := uint32(1), uint32(1)
		for !reach(high) {
			if high >= 1<<20 {
				return adminRefused("that level can't be reached")
			}
			low, high = high+1, high*2
		}
		for low < high {
			if mid := (low + high) / 2; reach(mid) {
				high = mid
			} else {
				low = mid + 1
			}
		}
		ship, err := tx.GetShip(tx.ctx, db.GetShipParams{ID: row.ShipID, CommanderID: tx.commanderID})
		if err != nil {
			return err
		}
		if need := before.addExp(config, high*config.itemExp()).needLevel(config, true); ship.Level < need {
			return adminRefused("the ship needs level %d", need)
		}
		return tx.developBlueprint(row.ShipID, high, true, func(blueprintConfig) error { return nil })
	})
}

// FleetTechGroup is a faction's Fleet Technology: its levels researched, the
// level being researched (0 none) and when it ends, the last level whose
// reward was claimed, and the faction's tech points.
type FleetTechGroup struct {
	ID         uint32 `json:"id"`
	Nation     uint32 `json:"nation"`
	Level      int    `json:"level"`
	MaxLevel   int    `json:"max_level"`
	Study      int    `json:"study"`
	StudyEnd   int64  `json:"study_end"`
	StudyTime  int64  `json:"study_time"`
	Rewarded   int    `json:"rewarded"`
	Points     uint32 `json:"points"`
	NextPoints uint32 `json:"next_points"` // the next level's pt, 0 at the max
}

// FleetTechAttr is an attribute bonus of a ship type: its max (what the
// groups and ships give) and the value set (the max unless lowered).
type FleetTechAttr struct {
	ShipType uint32 `json:"ship_type"`
	Attr     uint32 `json:"attr"`
	Max      uint32 `json:"max"`
	Set      uint32 `json:"set"`
}

// FleetTech is the commander's Fleet Technology. Counting the tech points
// raises the ship groups' records (techShipGroups); that is rolled back.
func (game *Game) FleetTech(ctx context.Context, id uint32) (groups []FleetTechGroup, attrs []FleetTechAttr, err error) {
	errRead := errors.New("read only")
	err = game.DB.InTx(ctx, func(queries *db.Queries) error {
		tx := newTx(ctx, queries, id)
		techs, err := tx.fleetTechs()
		if err != nil {
			return err
		}
		points, err := techPoints(ctx, queries, id)
		if err != nil {
			return err
		}
		owned, err := techShipGroups(ctx, queries, id)
		if err != nil {
			return err
		}
		for groupID, group := range fleetTechGroups {
			tech := techs[groupID]
			row := FleetTechGroup{ID: groupID, Level: fleetTechIndex(group, tech.EffectTechID), MaxLevel: len(group.Techs), Rewarded: fleetTechIndex(group, tech.RewardedTechID)}
			if len(group.Nation) > 0 {
				row.Nation, row.Points = group.Nation[0], points[group.Nation[0]]
			}
			if tech.StudyTechID != 0 {
				row.Study, row.StudyEnd, row.StudyTime = fleetTechIndex(group, tech.StudyTechID), tech.StudyFinishAt, int64(fleetTechLevels[tech.StudyTechID].Time)
			}
			if row.Level < len(group.Techs) {
				row.NextPoints = fleetTechLevels[group.Techs[row.Level]].Pt
			}
			groups = append(groups, row)
		}
		set, err := tx.ListFleetTechAttrs(ctx, id)
		if err != nil {
			return err
		}
		for shipType, bonuses := range fleetTechMaxBonuses(owned, techs) {
			for attr, top := range bonuses {
				row := FleetTechAttr{ShipType: shipType, Attr: attr, Max: top, Set: top}
				for _, s := range set {
					if s.ShipType == shipType && s.AttrType == attr {
						row.Set = s.SetValue
					}
				}
				attrs = append(attrs, row)
			}
		}
		return errRead
	})
	if errors.Is(err, errRead) {
		err = nil
	}
	return groups, attrs, err
}

// SetFleetTechLevel sets how many levels of a faction's Fleet Technology are
// researched, 0 to its last, while none of it is being researched. The
// claimed rewards and the lowered bonuses stay within the new level.
func (game *Game) SetFleetTechLevel(ctx context.Context, id, groupID uint32, level int) (bool, error) {
	group, ok := fleetTechGroups[groupID]
	if !ok {
		return false, adminRefused("no fleet tech group %d", groupID)
	}
	if level < 0 || level > len(group.Techs) {
		return false, adminRefused("the level goes from 0 to %d", uint32(len(group.Techs)))
	}
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		tech, err := tx.fleetTech(groupID)
		if err != nil {
			return err
		}
		if tech.StudyTechID != 0 {
			return adminRefused("a level of it is being researched")
		}
		tech.EffectTechID = 0
		if level > 0 {
			tech.EffectTechID = group.Techs[level-1]
		}
		if fleetTechIndex(group, tech.RewardedTechID) > level {
			tech.RewardedTechID = tech.EffectTechID
		}
		if err := tx.saveFleetTech(tech); err != nil {
			return err
		}
		return tx.clampFleetTechAttrs()
	})
}

// clampFleetTechAttrs lowers the bonuses set above what the commander now
// has to that.
func (tx *Tx) clampFleetTechAttrs() error {
	owned, err := techShipGroups(tx.ctx, tx.Queries, tx.commanderID)
	if err != nil {
		return err
	}
	techs, err := tx.fleetTechs()
	if err != nil {
		return err
	}
	bonuses := fleetTechMaxBonuses(owned, techs)
	set, err := tx.ListFleetTechAttrs(tx.ctx, tx.commanderID)
	if err != nil {
		return err
	}
	if err := tx.DeleteFleetTechAttrs(tx.ctx, tx.commanderID); err != nil {
		return err
	}
	for _, row := range set {
		top, ok := bonuses[row.ShipType][row.AttrType]
		if !ok || row.SetValue >= top {
			continue
		}
		err := tx.AddFleetTechAttr(tx.ctx, db.AddFleetTechAttrParams{CommanderID: tx.commanderID, ShipType: row.ShipType, AttrType: row.AttrType, SetValue: row.SetValue})
		if err != nil {
			return err
		}
	}
	return nil
}

// ResetFleetTechAttrs puts a ship type's bonuses back to their max.
func (game *Game) ResetFleetTechAttrs(ctx context.Context, id, shipType uint32) (bool, error) {
	return game.progressEdit(ctx, id, func(tx *Tx, now time.Time) error {
		return tx.ProgressDeleteFleetTechAttrsOfType(tx.ctx, db.ProgressDeleteFleetTechAttrsOfTypeParams{CommanderID: tx.commanderID, ShipType: shipType})
	})
}
