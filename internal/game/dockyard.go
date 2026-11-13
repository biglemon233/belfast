package game

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// ship_data_breakout row: a limit break from the ship's template to
// breakout_id (0 at the last star), from level on, for use_char_num ships of
// its group (or Bulins, testShips), use_gold coins and use_item items.
type breakoutConfig struct {
	BreakoutID uint32     `json:"breakout_id"`
	Level      uint32     `json:"level"`
	UseCharNum uint32     `json:"use_char_num"`
	UseGold    uint32     `json:"use_gold"`
	UseItem    [][]uint32 `json:"use_item"`
}

// ship_data_strengthen row, by ShipModAttr.ATTR_TO_INDEX (cannon, torpedo,
// anti-air, air, reload): the exp a ship gives as an enhancement material
// (attr_exp), how many points it can be enhanced (durability, scaled by
// its level, Ship:getModAttrTopLimit) and the exp a point takes
// (level_exp, Ship:getModExpRatio).
type strengthenConfig struct {
	AttrExp    []uint32 `json:"attr_exp"`
	Durability []uint32 `json:"durability"`
	LevelExp   []uint32 `json:"level_exp"`
}

// ship_level row: the level_limit levels are the max levels Cognitive
// Awakening raises a ship to, need_item_rarity<N> what raising it past this
// level costs a ship of rarity N (Ship:getNextMaxLevelConsume).
type shipLevelLimitConfig struct {
	Level      uint32     `json:"level"`
	LevelLimit uint32     `json:"level_limit"`
	Rarity2    [][]uint32 `json:"need_item_rarity2"`
	Rarity3    [][]uint32 `json:"need_item_rarity3"`
	Rarity4    [][]uint32 `json:"need_item_rarity4"`
	Rarity5    [][]uint32 `json:"need_item_rarity5"`
	Rarity6    [][]uint32 `json:"need_item_rarity6"`
}

// ShipModAttr.ID_TO_ATTR (model/const/shipmodattr.lua): the strength_list
// ids and the AttributeType they enhance, in ATTR_TO_INDEX order.
var shipModAttrs = []struct {
	id   uint32
	name string
}{{2, "cannon"}, {3, "torpedo"}, {4, "antiaircraft"}, {5, "air"}, {6, "reload"}}

const (
	// ShipModLayer.IGNORE_ID: the attribute materials give no exp to
	// (ShipModLayer.getModExpAdditions)
	shipModIgnoredAttr = 4
	// DockyardScene's selectedMax for enhancement materials (ShipModMediator)
	shipModMaxMaterials = 12
	// ShipModLayer.getModExpAdditions: a material of the ship's own group
	// gives twice its attr_exp
	shipModSameGroupRate = 2
	// BuffUsageConst.SHIP_MOD_EXP, the benefit_buff_template benefit_type
	// raising enhancement exp by benefit_effect %
	benefitShipModExp = "character_strengthen_exp"
)

var (
	breakouts   map[uint32]breakoutConfig   // by ship_data_template id
	strengthens map[uint32]strengthenConfig // by strengthen_id
	// levelLimits are the ship_level level_limit rows, by level
	levelLimits map[uint32]shipLevelLimitConfig
	// shipConfigMaxLevel is Ship:getConfigMaxLevel, the last ship_level
	shipConfigMaxLevel uint32
	// testShips are the Bulins (gameset test_ship_config_1..3): the
	// rarities a limit break takes them for instead of a copy (Ship:Ctor
	// testShip, Ship:canUseTestShip)
	testShips map[uint32][]uint32
	// shipModExpBuffs are the benefit_effect (%) of the SHIP_MOD_EXP
	// benefit_buff_template rows
	shipModExpBuffs map[uint32]float64
)

// testShipRarities are the rarities each Bulin stands in for, in
// test_ship_config order (Ship:Ctor): the Universal Bulin up to Elite, the
// Prototype Bulin MKII for Super Rare, the Specialized Bulin Custom MKIII
// for Ultra Rare.
var testShipRarities = [][]uint32{{2, 3, 4}, {5}, {6}}

func loadDockyardData() error {
	var levels map[uint32]shipLevelLimitConfig
	var gameset map[string]uint32
	var buffs map[uint32]struct {
		BenefitType   string `json:"benefit_type"`
		BenefitEffect string `json:"benefit_effect"`
	}
	err := gamedata.LoadAll(
		gamedata.Load("ship_data_breakout", &breakouts),
		gamedata.Load("ship_data_strengthen", &strengthens),
		gamedata.Load("ship_level", &levels),
		gamedata.Load("benefit_buff_template", &buffs),
		func() (err error) {
			gameset, err = gamedata.Gameset()
			return err
		},
	)
	if err != nil {
		return err
	}
	testShips = map[uint32][]uint32{}
	for i, rarities := range testShipRarities {
		key := fmt.Sprintf("test_ship_config_%d", i+1)
		id, ok := gameset[key]
		if !ok {
			return gamedata.Missing("gameset", "%s", key)
		}
		testShips[id] = rarities
	}
	levelLimits = map[uint32]shipLevelLimitConfig{}
	shipConfigMaxLevel = 0
	for _, row := range levels {
		shipConfigMaxLevel = max(shipConfigMaxLevel, row.Level)
		if row.LevelLimit == 1 {
			levelLimits[row.Level] = row
		}
	}
	shipModExpBuffs = map[uint32]float64{}
	for id, buff := range buffs {
		if buff.BenefitType != benefitShipModExp {
			continue
		}
		if n, err := strconv.ParseFloat(buff.BenefitEffect, 64); err == nil {
			shipModExpBuffs[id] = n
		}
	}
	return nil
}

// specialShip is whether a ship is a blueprint (Priority / Decisive) or
// META ship: they are limit broken and enhanced through their own systems,
// never through the dock's 12017 / 12027.
func specialShip(templateID uint32) bool {
	return ownableShips[templateID].unretirable
}

// shipsInChapter are the commander's ships in the active chapter's fleets
// (ShipStatus inChapter), which onModify refuses.
func shipsInChapter(ctx context.Context, queries *db.Queries, commanderID uint32) (map[uint32]bool, error) {
	chapter, err := loadChapter(ctx, queries, commanderID)
	if err != nil || chapter == nil {
		return nil, err
	}
	in := map[uint32]bool{}
	info := chapter.info
	for _, group := range slices.Concat(info.GetMainGroupList(), info.GetSubmarineGroupList(), info.GetSupportGroupList()) {
		for _, ship := range group.GetShipList() {
			in[ship.GetId()] = true
		}
	}
	return in, nil
}

// modifiableShip is the commander's ship id a limit break or enhancement is
// for: errRefused when it isn't theirs or is in the active
// chapter (ShipStatus.ShipStatusCheck "onModify").
func (tx *Tx) modifiableShip(id uint32) (db.Ship, error) {
	ship, err := tx.GetShip(tx.ctx, db.GetShipParams{ID: id, CommanderID: tx.commanderID})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Ship{}, errRefused
	} else if err != nil {
		return db.Ship{}, err
	}
	busy, err := shipsInChapter(tx.ctx, tx.Queries, tx.commanderID)
	if err != nil {
		return db.Ship{}, err
	}
	if busy[id] {
		return db.Ship{}, errRefused
	}
	return ship, nil
}

// materialShips loads the ships of ids fed to target, refusing the lot
// unless every one is the commander's, given once, not target itself and
// one the dock lets go (ShipStatus.canDestroyShip, as for retirement: not
// locked, blueprint or META, nor in a fleet, the active chapter or a
// secretary slot).
func (tx *Tx) materialShips(target uint32, ids []uint32) ([]db.Ship, error) {
	if len(ids) == 0 || slices.Contains(ids, target) || len(slices.Compact(slices.Sorted(slices.Values(ids)))) != len(ids) {
		return nil, errRefused
	}
	byID, err := shipsByID(tx.ctx, tx.Queries, tx.commanderID, ids)
	if err != nil {
		return nil, err
	}
	busy, err := busyShips(tx.ctx, tx.Queries, tx.commanderID)
	if err != nil {
		return nil, err
	}
	ships := make([]db.Ship, len(ids))
	for i, id := range ids {
		ship, ok := byID[id]
		template, known := ownableShips[ship.TemplateID]
		if !ok || !known || ship.IsLocked || template.unretirable || busy[id] {
			return nil, errRefused
		}
		ships[i] = ship
	}
	return ships, nil
}

// upgradeMaterial is whether material can be fed to ship's limit break or
// retrofit (BayProxy:getUpgradeShips): a ship of its group, or a Bulin
// standing in for its rarity.
func upgradeMaterial(ship, material db.Ship) bool {
	return shipGroup(material.TemplateID) == shipGroup(ship.TemplateID) ||
		slices.Contains(testShips[material.TemplateID], ownableShips[ship.TemplateID].Rarity)
}

// consumeShips removes material ships, their gear back to the depot and
// their gear skins to their box: what the client does on result 0 of 12017
// and 12027.
func (tx *Tx) consumeShips(ships []db.Ship) error {
	for _, ship := range ships {
		equips, err := tx.ListShipEquipments(tx.ctx, ship.ID)
		if err != nil {
			return err
		}
		for _, equip := range equips {
			if equip.EquipID != 0 {
				if err := tx.AddEquipment(equip.EquipID, 1); err != nil {
					return err
				}
			}
		}
		if err := tx.unequipSkins(equips); err != nil {
			return err
		}
		// the archives keep what it reached
		if _, err := raiseShipGroup(tx.ctx, tx.Queries, ship); err != nil {
			return err
		}
		if err := tx.DeleteShip(tx.ctx, db.DeleteShipParams{ID: ship.ID, CommanderID: tx.commanderID}); err != nil {
			return err
		}
	}
	return nil
}

// payDrops takes [type, id, count] costs: player_resource or bag items.
func (tx *Tx) payDrops(costs [][]uint32) error {
	for _, cost := range costs {
		if len(cost) < 3 {
			return errRefused
		}
		var err error
		switch cost[0] {
		case dropTypeResource:
			err = tx.SpendResource(cost[1], cost[2])
		case dropTypeItem:
			err = tx.SpendItem(cost[1], cost[2])
		default:
			err = errRefused
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// saveShipGrowth stores a ship's template, level, exp and max level, and
// raises its archive entry: a new star (limit break) or level counts for
// Fleet Technology.
func (tx *Tx) saveShipGrowth(ship db.Ship) error {
	if err := tx.UpdateShipGrowth(tx.ctx, db.UpdateShipGrowthParams{
		TemplateID: ship.TemplateID, Level: ship.Level, Exp: ship.Exp, MaxLevel: ship.MaxLevel, ID: ship.ID, CommanderID: tx.commanderID,
	}); err != nil {
		return err
	}
	raised, err := raiseShipGroup(tx.ctx, tx.Queries, ship)
	if raised {
		tx.techPointsChanged()
	}
	return err
}

// handleUpgradeStar answers UpgradeStarCommand (CS_12027,
// controller/command/ship/upgradestarcommand.lua, ShipUpgradeLayer2): limit
// breaks ship_id to its ship_data_breakout breakout_id, from the row's
// level, for its use_char_num material ships, use_gold coins and use_item.
// A material is a ship of the same group or a Bulin standing in for the
// ship's rarity (BayProxy:getUpgradeShips), which the dock lets go
// (ShipUpgradeMediator2 onShip: ShipStatus.canDestroyShip). On result 0
// the client removes the materials (their gear to the depot), the coins and
// items, and moves the ship to the new template: the skills of its buff_list
// it lacks at level 1, the skills only the old template had dropped, its
// max level raised to the new template's (Ship:updateMaxLevel). It checks
// the new template's gear slots itself before sending. No official capture
// has it: built from the Lua alone.
func (game *Game) handleUpgradeStar(client *connection.Client, req *protobuf.CS_12027) error {
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		ship, err := tx.modifiableShip(req.GetShipId())
		if err != nil {
			return err
		}
		breakout, ok := breakouts[ship.TemplateID]
		_, known := ownableShips[breakout.BreakoutID]
		if !ok || breakout.BreakoutID == 0 || !known || specialShip(ship.TemplateID) || ship.Level < breakout.Level {
			return errRefused
		}
		if uint32(len(req.GetMaterialIdList())) != breakout.UseCharNum {
			return errRefused
		}
		materials, err := tx.materialShips(ship.ID, req.GetMaterialIdList())
		if err != nil {
			return err
		}
		for _, material := range materials {
			if !upgradeMaterial(ship, material) {
				return errRefused
			}
		}
		if err := tx.SpendResource(ResourceGold, breakout.UseGold); err != nil {
			return err
		}
		for _, item := range breakout.UseItem {
			if len(item) < 2 {
				return errRefused
			}
			if err := tx.SpendItem(item[0], item[1]); err != nil {
				return err
			}
		}
		if err := tx.consumeShips(materials); err != nil {
			return err
		}
		if err := tx.limitBreak(ship, breakout.BreakoutID); err != nil {
			return err
		}
		// the commander manual counts breakouts, and how far one went
		tx.event(TaskEvent{SubType: TaskSubBreakout, Count: 1})
		if n := breakout.BreakoutID % 10; n > 1 {
			tx.event(TaskEvent{SubType: TaskSubBreakoutStar, Count: n - 1})
		}
		return nil
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_12028{Result: result(err)})
}

// limitBreak moves ship to its limit broken template next, as the client
// does on a limit break (UpgradeStarCommand, and ModBluePrintCommand's
// upgradeStar for research ships): the skills of next's buff_list it lacks
// at level 1, the skills only the old template had dropped, its max level
// raised to next's (Ship:updateMaxLevel).
func (tx *Tx) limitBreak(ship db.Ship, next uint32) error {
	template, ok := ownableShips[next]
	if !ok {
		return errRefused
	}
	for _, skill := range template.Skills {
		if err := tx.AddShipSkill(tx.ctx, db.AddShipSkillParams{ShipID: ship.ID, SkillID: skill, Level: 1}); err != nil {
			return err
		}
	}
	for _, skill := range ownableShips[ship.TemplateID].Skills {
		if !slices.Contains(template.Skills, skill) {
			if err := tx.DeleteShipSkill(tx.ctx, db.DeleteShipSkillParams{ShipID: ship.ID, SkillID: skill}); err != nil {
				return err
			}
		}
	}
	ship.TemplateID = next
	ship.MaxLevel = max(min(shipConfigMaxLevel, template.MaxLevel), ship.MaxLevel)
	return tx.saveShipGrowth(ship)
}

// nextMaxLevel is Ship:getNextMaxLevel: the first ship_level level_limit
// level above the ship's max level, false past the last one.
func nextMaxLevel(ship db.Ship) (uint32, bool) {
	for level := ship.MaxLevel + 1; level <= shipConfigMaxLevel; level++ {
		if _, ok := levelLimits[level]; ok {
			return level, true
		}
	}
	return 0, false
}

// awakeningCost is Ship:getNextMaxLevelConsume: the ship_level row of the
// ship's max level, its need_item_rarity of the ship's rarity.
func awakeningCost(ship db.Ship) ([][]uint32, bool) {
	row, ok := levelLimits[ship.MaxLevel]
	if !ok {
		return nil, false
	}
	switch ownableShips[ship.TemplateID].Rarity {
	case 2:
		return row.Rarity2, true
	case 3:
		return row.Rarity3, true
	case 4:
		return row.Rarity4, true
	case 5:
		return row.Rarity5, true
	case 6:
		return row.Rarity6, true
	}
	return nil, false
}

// handleUpgradeMaxLevel answers UpgradeShipMaxLevelCommand (CS_12038,
// controller/command/ship/upgradeshipmaxlevelcommand.lua): Cognitive
// Awakening. Ship:canUpgradeMaxLevel offers it to a ship at its max level
// that keeps exp past it (can_get_proficency) while a higher level_limit
// level is left (Ship:isReachNextMaxLevel); it costs the ship_level
// need_item_rarity of its rarity. On result 0 the client takes the costs,
// raises the max level to the next one and adds the exp kept
// (Ship:addExp(0, true)), which may level the ship up at once. No official
// capture has it: built from the Lua alone.
func (game *Game) handleUpgradeMaxLevel(client *connection.Client, req *protobuf.CS_12038) error {
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		// no onModify check here (ShipDetailLogicPanel)
		ship, err := tx.GetShip(tx.ctx, db.GetShipParams{ID: req.GetShipId(), CommanderID: tx.commanderID})
		if errors.Is(err, sql.ErrNoRows) {
			return errRefused
		} else if err != nil {
			return err
		}
		next, ok := nextMaxLevel(ship)
		cost, priced := awakeningCost(ship)
		if !ok || !priced || ship.Level != ship.MaxLevel || !shipLevelings[ship.TemplateID].accumulate {
			return errRefused
		}
		if err := tx.payDrops(cost); err != nil {
			return err
		}
		before := ship.Level
		ship.MaxLevel = next
		addShipExp(&ship, 0)
		if before < techShipLevel && ship.Level >= techShipLevel {
			tx.techPointsChanged()
		}
		return tx.saveShipGrowth(ship)
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_12039{Result: result(err)})
}

// shipModExpBuff is ShipModLayer.getBuffExp: the highest SHIP_MOD_EXP
// benefit of the buff activities open (BuffHelper.GetShipModExpBuff), as a
// rate. Belfast gives commanders no buffs of their own.
func shipModExpBuff(now time.Time) float64 {
	rate := 0.0
	for _, config := range openActivities(now) {
		if config.Type != activityTypeBuff {
			continue
		}
		buffs := []uint32{config.ConfigID}
		if config.ConfigID == 0 {
			buffs = gamedata.IDs(config.ConfigData)
		}
		for _, buff := range buffs {
			rate = max(rate, shipModExpBuffs[buff]/100)
		}
	}
	return rate
}

// shipModTopLimit is Ship:getModAttrTopLimit: the points attribute i (an
// index of shipModAttrs) of a ship can be enhanced, its durability scaled
// from x0.3 to x1 by its level up to 100.
func shipModTopLimit(ship db.Ship, strengthen strengthenConfig, i int) uint32 {
	if i >= len(strengthen.Durability) {
		return 0
	}
	return uint32(calcFloor((3 + 7*float64(min(ship.Level, 100))/100) * float64(strengthen.Durability[i]) * 0.1))
}

// shipModExpRatio is Ship:getModExpRatio: the exp a point takes, 1 for an
// attribute the ship's statistics lock.
func shipModExpRatio(ship db.Ship, strengthen strengthenConfig, i int) uint32 {
	if slices.Contains(ownableShips[ship.TemplateID].locked, shipModAttrs[i].name) || i >= len(strengthen.LevelExp) {
		return 1
	}
	return max(strengthen.LevelExp[i], 1)
}

// handleModShip answers ModShipCommand (CS_12017,
// controller/command/ship/modshipcommand.lua, ShipModLayer): enhances
// ship_id with up to 12 material ships the dock lets go (ShipModMediator
// onShip: ShipStatus.canDestroyShip). Each gives the attr_exp of its
// ship_data_strengthen to every attribute but anti-air
// (ShipModLayer.getModExpAdditions), twice when it is of the ship's group,
// the total raised by the open SHIP_MOD_EXP buffs and floored; the ship
// keeps it up to its top limit (Ship:addModAttrExp). On result 0 the
// client removes the materials (their gear to the depot) and adds the same
// exp. No official capture has it: built from the Lua alone.
func (game *Game) handleModShip(client *connection.Client, req *protobuf.CS_12017) error {
	now := time.Now()
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		ship, err := tx.modifiableShip(req.GetShipId())
		if err != nil {
			return err
		}
		strengthen, ok := strengthens[ownableShips[ship.TemplateID].StrengthenID]
		if !ok || specialShip(ship.TemplateID) || len(req.GetMaterialIdList()) > shipModMaxMaterials {
			return errRefused
		}
		materials, err := tx.materialShips(ship.ID, req.GetMaterialIdList())
		if err != nil {
			return err
		}
		group := shipGroup(ship.TemplateID)
		gains := make([]float64, len(shipModAttrs))
		for _, material := range materials {
			given, ok := strengthens[ownableShips[material.TemplateID].StrengthenID]
			if !ok {
				return errRefused
			}
			for i, attr := range shipModAttrs {
				if attr.id == shipModIgnoredAttr || i >= len(given.AttrExp) {
					continue
				}
				exp := given.AttrExp[i]
				if shipGroup(material.TemplateID) == group {
					exp *= shipModSameGroupRate
				}
				gains[i] += float64(exp)
			}
		}
		rows, err := tx.ListShipStrengths(tx.ctx, ship.ID)
		if err != nil {
			return err
		}
		current := map[uint32]uint32{}
		for _, row := range rows {
			current[row.AttrID] = row.Exp
		}
		buff := shipModExpBuff(now)
		for i, attr := range shipModAttrs {
			limit := shipModTopLimit(ship, strengthen, i)
			if limit == 0 {
				continue
			}
			ceiling := limit * shipModExpRatio(ship, strengthen, i)
			exp := current[attr.id] + uint32(math.Floor(gains[i]*(1+buff)))
			if exp > ceiling {
				exp = ceiling
			}
			if exp == current[attr.id] {
				continue
			}
			if err := tx.SetShipStrength(tx.ctx, db.SetShipStrengthParams{ShipID: ship.ID, AttrID: attr.id, Exp: exp}); err != nil {
				return err
			}
		}
		// the event tasks count the enhancement ("舰船强化5次")
		tx.event(TaskEvent{SubType: TaskSubShipEnhance, Count: 1})
		return tx.consumeShips(materials)
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_12018{Result: result(err)})
}

// strengthInfo is SHIPINFO.strength_list from a ship's ship_strengths.
func strengthInfo(rows []db.ShipStrength) []*protobuf.STRENGTH_INFO {
	list := make([]*protobuf.STRENGTH_INFO, len(rows))
	for i, row := range rows {
		list[i] = &protobuf.STRENGTH_INFO{Id: proto.Uint32(row.AttrID), Exp: proto.Uint32(row.Exp)}
	}
	return list
}
