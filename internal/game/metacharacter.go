package game

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// A META ship (META Lab, view/technology/metacharacter) grows through its
// ship_strengthen_meta row (its group, MetaCharacterConst.GetMetaShipGroupIDByConfigID):
// Rigging Fortification (MetaCharacterRepairLayer) raises 4 attributes one
// ship_meta_repair row at a time, Somatic Activation (MetaCharacterEnergyLayer)
// moves it along its ship_meta_breakout rows, and Tactical Research
// (metatactics.go) levels its research skills, with the META skill books
// too (MetaQuickTacticsLayer). No official capture has any of it: the
// replies are built from the Lua alone.

// itemTypeMetaLesson is Item.METALESSON_TYPE (model/vo/item.lua), the META
// skill books.
const itemTypeMetaLesson = 25

// ship_meta_breakout row: a META ship's next template (0 at the last one),
// from level on, once its repair exp reaches repair, for gold coins and
// item1_num item1 (MetaCharacterBreakout; item2 is never read).
type metaBreakout struct {
	BreakoutID uint32 `json:"breakout_id"`
	Level      uint32 `json:"level"`
	Repair     uint32 `json:"repair"`
	Gold       uint32 `json:"gold"`
	Item1      uint32 `json:"item1"`
	Item1Num   uint32 `json:"item1_num"`
}

// ship_meta_repair row: one step of a Rigging Fortification attribute,
// item_num item_id for repair_exp (MetaRepairItem).
type metaRepair struct {
	ItemID    uint32 `json:"item_id"`
	ItemNum   uint32 `json:"item_num"`
	RepairExp uint32 `json:"repair_exp"`
}

var (
	// metaBreakouts by ship_data_template id: every META ship template
	// (Ship.isMetaShipByConfigID)
	metaBreakouts map[uint32]metaBreakout
	metaRepairs   map[uint32]metaRepair
	// metaLessonBooks is the exp each META skill book gives (its
	// usage_arg, MetaQuickTacticsLayer expDict)
	metaLessonBooks map[uint32]uint32
)

// A duplicate META ship becomes broken crystals instead of a second copy: the
// game's own reward for it ("已获得破碎的结晶 ×10", as its ship launch screen
// shows). The ship's own crystal (ship_meta_breakout item1) is a different
// item, spent by Somatic Activation.
const (
	// itemMetaBrokenCrystal is 破碎的结晶, item_data_statistics 21000
	itemMetaBrokenCrystal = 21000
	// metaBrokenCrystalsPerShip is how many a duplicate gives
	metaBrokenCrystalsPerShip = 10
)

// metaBrokenCrystals is how many broken crystals a duplicate of the META ship
// template gives, 0 for a ship that isn't a META.
func metaBrokenCrystals(templateID uint32) uint32 {
	if _, ok := metaBreakouts[templateID]; ok {
		return metaBrokenCrystalsPerShip
	}
	return 0
}

// ownsMetaShipGroup is whether the commander already has a ship of the META
// template's group: a ship limit broken along ship_meta_breakout keeps the
// group in its template id (MetaCharacterConst.GetMetaShipGroupIDByConfigID
// is the template id without its last digit).
func (tx *Tx) ownsMetaShipGroup(templateID uint32) (bool, error) {
	ships, err := tx.ListShipsByCommander(tx.ctx, tx.commanderID)
	if err != nil {
		return false, err
	}
	group := templateID / 10
	for _, ship := range ships {
		if ship.TemplateID/10 == group {
			return true, nil
		}
	}
	return false, nil
}

// readMetaLessonBooks reads the usage_arg of the item_data_statistics META
// skill books.
func readMetaLessonBooks(items map[uint32]itemConfig) {
	metaLessonBooks = map[uint32]uint32{}
	for id, row := range items {
		if row.Type != itemTypeMetaLesson {
			continue
		}
		if exp := gamedata.IDs(row.UsageArg); len(exp) == 1 {
			metaLessonBooks[id] = exp[0]
		}
	}
}

// metaShip is one of the commander's META ships, errRefused for any other
// ship.
func (tx *Tx) metaShip(id uint32) (db.Ship, error) {
	ship, err := tx.GetShip(tx.ctx, db.GetShipParams{ID: id, CommanderID: tx.commanderID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Ship{}, errRefused
	} else if err != nil {
		return db.Ship{}, err
	}
	if _, meta := metaBreakouts[ship.TemplateID]; !meta {
		return db.Ship{}, errRefused
	}
	return ship, nil
}

// metaRepairLevels is MetaCharacter's attribute levels from the ship's
// repairs: for each repair list, 1 past the furthest of its rows done
// (MetaCharacterAttr:getLevelByItemId, updateCount), and the repair exp of
// the rows below each level (MetaCharacter:getCurRepairExp).
func metaRepairLevels(config metaShipConfig, done []uint32) (levels [4]uint32, exp uint32) {
	for i, list := range config.repairs() {
		levels[i] = 1
		for j, id := range list {
			if slices.Contains(done, id) {
				levels[i] = uint32(j) + 2
			}
		}
		for _, id := range list[:levels[i]-1] {
			exp += metaRepairs[id].RepairExp
		}
	}
	return levels, exp
}

// setMetaRepairLists fills SHIPINFO.meta_repair_list (the ship_meta_repair
// rows done) of the commander's ships in list.
func setMetaRepairLists(ctx context.Context, queries *db.Queries, commanderID uint32, list []*protobuf.SHIPINFO) error {
	rows, err := queries.ListMetaRepairsByCommander(ctx, commanderID)
	if err != nil {
		return err
	}
	byShip := map[uint32][]uint32{}
	for _, row := range rows {
		byShip[row.ShipID] = append(byShip[row.ShipID], row.RepairID)
	}
	for _, info := range list {
		info.MetaRepairList = byShip[info.GetId()]
	}
	return nil
}

// handleMetaRepair answers MetaCharacterRepairCommand
// (controller/command/technology/metacharacter/metacharacterrepaircommand.lua),
// Rigging Fortification: repair_id is the next ship_meta_repair row of one of
// the META ship's attributes (MetaCharacterAttr:getItem, the row at its
// level), paid with its item_num item_id. On result 0 the client raises the
// attribute a level and takes the items off itself; the rows done come back
// at login as SHIPINFO.meta_repair_list.
func (game *Game) handleMetaRepair(client *connection.Client, req *protobuf.CS_63301) error {
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		ship, err := tx.metaShip(req.GetShipId())
		if err != nil {
			return err
		}
		config := metaShips[ship.TemplateID/10]
		done, err := tx.ListMetaRepairs(tx.ctx, ship.ID)
		if err != nil {
			return err
		}
		levels, _ := metaRepairLevels(config, done)
		next := false
		for i, list := range config.repairs() {
			if levels[i] <= uint32(len(list)) && list[levels[i]-1] == req.GetRepairId() {
				next = true
			}
		}
		repair, ok := metaRepairs[req.GetRepairId()]
		if !next || !ok {
			return errRefused
		}
		if err := tx.SpendItem(repair.ItemID, repair.ItemNum); err != nil {
			return err
		}
		return tx.AddMetaRepair(tx.ctx, db.AddMetaRepairParams{ShipID: ship.ID, RepairID: req.GetRepairId()})
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_63302{Result: result(err)})
}

// handleMetaEnergy answers MetaCharActiveEnergyCommand
// (metacharactiveenergycommand.lua), Somatic Activation, the META limit
// break: moves the ship to its ship_meta_breakout breakout_id from the row's
// level and repair exp, for its gold and item1_num item1 (the ship's
// crystals). On result 0 the client takes the costs and moves the ship to
// the new template as a limit break does (updateStar): the skills of its
// buff_list it lacks at level 1, the old template's buff_list skills it
// doesn't keep dropped (research skills are not in buff_list and stay), its
// max level raised to the new template's (Ship:updateMaxLevel), and its
// archive entry's star.
func (game *Game) handleMetaEnergy(client *connection.Client, req *protobuf.CS_63303) error {
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		ship, err := tx.metaShip(req.GetShipId())
		if err != nil {
			return err
		}
		breakout := metaBreakouts[ship.TemplateID]
		next, known := ownableShips[breakout.BreakoutID]
		if breakout.BreakoutID == 0 || !known || ship.Level < breakout.Level {
			return errRefused
		}
		done, err := tx.ListMetaRepairs(tx.ctx, ship.ID)
		if err != nil {
			return err
		}
		if _, exp := metaRepairLevels(metaShips[ship.TemplateID/10], done); exp < breakout.Repair {
			return errRefused
		}
		if err := tx.SpendResource(ResourceGold, breakout.Gold); err != nil {
			return err
		}
		if breakout.Item1Num > 0 {
			if err := tx.SpendItem(breakout.Item1, breakout.Item1Num); err != nil {
				return err
			}
		}
		for _, skill := range next.Skills {
			if err := tx.AddShipSkill(tx.ctx, db.AddShipSkillParams{ShipID: ship.ID, SkillID: skill, Level: 1}); err != nil {
				return err
			}
		}
		for _, skill := range ownableShips[ship.TemplateID].Skills {
			if !slices.Contains(next.Skills, skill) {
				if err := tx.DeleteShipSkill(tx.ctx, db.DeleteShipSkillParams{ShipID: ship.ID, SkillID: skill}); err != nil {
					return err
				}
			}
		}
		ship.TemplateID = breakout.BreakoutID
		ship.MaxLevel = max(min(shipConfigMaxLevel, next.MaxLevel), ship.MaxLevel)
		return tx.saveShipGrowth(ship)
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_63304{Result: result(err)})
}

// metaSkillMaxLevel is a research skill's skill_data_template max_level
// (Ship:isSkillLevelMax).
func metaSkillMaxLevel(skillID uint32) uint32 {
	return skills[skillID].MaxLevel
}

// handleMetaTacticsLevelUp answers MetaCharacterTacticsLevelUpCommand
// (metacharactertacticslevelupcommand.lua): raises a research skill whose
// exp reached its ship_meta_skilltask need_exp a level. On result 0 the
// client raises the skill (Ship:upSkillLevelForMeta), sets its exp to 0
// (MetaCharacterTacticsLayer:levelupTacticsSkillData) and shows switch_cnt
// as the switches left today. Nothing in the EN client sends it (no
// GAME.TACTICS_META_LEVELUP_SKILL notification is ever raised): SC_63316
// carries the research levels.
func (game *Game) handleMetaTacticsLevelUp(client *connection.Client, req *protobuf.CS_63309) error {
	day := metaDay(time.Now())
	var left uint32
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		t, err := tx.loadOneMetaTactics(req.GetShipId(), day)
		if err != nil {
			return err
		}
		left = metaSwitchesPerDay - min(t.state.Switches, metaSwitchesPerDay)
		skill := req.GetSkillId()
		level := t.skillLevel(skill)
		if !t.isResearchSkill(skill) || level == 0 || level >= metaSkillMaxLevel(skill) {
			return errRefused
		}
		task, ok := metaSkillTasksByLevel[skill][level]
		exp := uint32(0)
		for _, s := range t.skills {
			if s.SkillID == skill {
				exp = s.Exp
			}
		}
		if !ok || exp < task.NeedExp {
			return errRefused
		}
		return tx.SetShipSkillExp(tx.ctx, db.SetShipSkillExpParams{Level: level + 1, Exp: 0, ShipID: t.ship.ID, SkillID: skill})
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_63310{Result: result(err), SwitchCnt: proto.Uint32(left)})
}

// handleMetaQuickTactics answers MetaQuickTacticsCommand
// (metaquicktacticscommand.lua): feeds META skill books (ITEM_INFO id/num,
// each worth its usage_arg exp) to an unlocked research skill below its max
// level. The exp levels the skill through its ship_meta_skilltask need_exp
// as MetaQuickTacticsLayer:calcLevelWithAwardExp counts it; what passes the
// max level is lost (MetaQuickTacticsOverflowLayer warns of it). On ret 0
// the client sets the skill to level and exp and takes the books off itself.
func (game *Game) handleMetaQuickTactics(client *connection.Client, req *protobuf.CS_63319) error {
	day := metaDay(time.Now())
	reply := &protobuf.SC_63320{}
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		t, err := tx.loadOneMetaTactics(req.GetShipId(), day)
		if err != nil {
			return err
		}
		skill := req.GetSkillId()
		level := t.skillLevel(skill)
		top := metaSkillMaxLevel(skill)
		if !t.isResearchSkill(skill) || level == 0 || level >= top {
			return errRefused
		}
		award := uint32(0)
		for _, book := range req.GetBooks() {
			exp, ok := metaLessonBooks[book.GetId()]
			if !ok {
				return errRefused
			}
			if book.GetNum() == 0 {
				continue
			}
			if err := tx.SpendItem(book.GetId(), book.GetNum()); err != nil {
				return err
			}
			award += exp * book.GetNum()
		}
		if award == 0 {
			return errRefused
		}
		exp := award
		for _, s := range t.skills {
			if s.SkillID == skill {
				exp += s.Exp
			}
		}
		for level < top {
			task, ok := metaSkillTasksByLevel[skill][level]
			if !ok || exp < task.NeedExp {
				break
			}
			exp -= task.NeedExp
			level++
		}
		if level >= top {
			exp = 0
		}
		reply.Level, reply.Exp = proto.Uint32(level), proto.Uint32(exp)
		return tx.SetShipSkillExp(tx.ctx, db.SetShipSkillExpParams{Level: level, Exp: exp, ShipID: t.ship.ID, SkillID: skill})
	})
	if err != nil && !refused(err) {
		return err
	}
	reply.Ret = result(err)
	return client.Send(reply)
}
