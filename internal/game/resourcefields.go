package game

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// fieldUpgrade is what an academy field's template row asks to go from its
// level to the next (BaseResourceField:CanUpgrade, NavalAcademyProxy
// StartUpGradeSuccess): the commander's user_level, use[2] of resource
// use[1] and time seconds. The last row is the max level.
type fieldUpgrade struct {
	Time      int64    `json:"time"`
	UserLevel uint32   `json:"user_level"`
	Use       []uint32 `json:"use"`
}

func (row fieldUpgrade) upgrade() fieldUpgrade { return row }

// resourceField is an oilfield_template / tradingport_template row: every
// hour_time-th of an hour the field produces production, up to store
// (BaseResourceField:getHourProduct).
type resourceField struct {
	HourTime   uint32 `json:"hour_time"`
	Production uint32 `json:"production"`
	Store      uint32 `json:"store"`
	fieldUpgrade
}

var oilFields, goldFields map[uint32]resourceField

// classField is a class_upgrade_template row, the lecture hall at a level
// (ClassResourceField): it turns proficiency_cost_per_min proficiency a
// minute into proficency_to_exp_rant percent of it in expField, up to stock
// of its item_id (an EXP Data Pack, usage_arg exp each). Battles give it
// proficency_get_percent of some ships' exp as proficiency, up to store
// (GetExp2ProficiencyRatio, GetMaxProficiency).
type classField struct {
	Item       uint32 `json:"item_id"`
	Stock      uint32 `json:"stock"`
	CostPerMin uint32 `json:"proficency_cost_per_min"`
	ExpRate    uint32 `json:"proficency_to_exp_rant"`
	GetPercent uint32 `json:"proficency_get_percent"`
	Store      uint32 `json:"store"`
	fieldUpgrade
}

// classBook is the item a classField makes: exp is its usage_arg
// (ClassResourceField:GetTarget), the expField one pack takes, and maxNum
// how many the bag holds (GetCanGetResCnt).
type classBook struct {
	exp, maxNum uint32
}

var (
	classFields map[uint32]classField
	classBooks  map[uint32]classBook
	// classOpenLevel is the commander level the lecture hall opens at:
	// open_systems_limited's ClassMediator row, below which the client
	// locks the hall's scene and its EXP Data Pack bubble in the
	// commission panel (SystemOpenMgr:isOpenSystem)
	classOpenLevel uint32
)

// the shop_template effect_args of the academy field upgrades (ShopArgs)
const (
	effectOilFieldLevel   = "oilfield_level"
	effectGoldFieldLevel  = "tradingport_level"
	effectClassFieldLevel = "class_room_level"
)

// fieldTable is an academy field's upgrades by level and its max level,
// the table's last (BaseResourceField:IsMaxLevel).
type fieldTable struct {
	rows map[uint32]fieldUpgrade
	max  uint32
}

// fieldUpgrades are the upgradable fields' tables by the effect_args of
// their upgrade.
var fieldUpgrades map[string]fieldTable

func upgradeTable[T interface{ upgrade() fieldUpgrade }](name string, rows map[uint32]T) (fieldTable, error) {
	table := fieldTable{rows: make(map[uint32]fieldUpgrade, len(rows))}
	for level, row := range rows {
		table.rows[level] = row.upgrade()
		table.max = max(table.max, level)
	}
	for level, row := range table.rows {
		if level != table.max && len(row.Use) < 2 {
			return fieldTable{}, gamedata.Missing(name, "use of level %d", level)
		}
	}
	return table, nil
}

func loadResourceFields() error {
	var items map[uint32]struct {
		UsageArg json.RawMessage `json:"usage_arg"`
		MaxNum   uint32          `json:"max_num"`
	}
	var systems map[uint32]struct {
		Mediator string `json:"mediator"`
		Level    uint32 `json:"level"`
	}
	err := gamedata.LoadAll(
		gamedata.Load("oilfield_template", &oilFields),
		gamedata.Load("tradingport_template", &goldFields),
		gamedata.Load("class_upgrade_template", &classFields),
		gamedata.Load("item_data_statistics", &items),
		gamedata.Load("open_systems_limited", &systems),
	)
	if err != nil {
		return err
	}
	classOpenLevel = 0
	for _, row := range systems {
		if row.Mediator == "ClassMediator" {
			classOpenLevel = row.Level
		}
	}
	if classOpenLevel == 0 {
		return gamedata.Missing("open_systems_limited", "ClassMediator")
	}
	oil, err := upgradeTable("oilfield_template", oilFields)
	if err != nil {
		return err
	}
	gold, err := upgradeTable("tradingport_template", goldFields)
	if err != nil {
		return err
	}
	class, err := upgradeTable("class_upgrade_template", classFields)
	if err != nil {
		return err
	}
	fieldUpgrades = map[string]fieldTable{effectOilFieldLevel: oil, effectGoldFieldLevel: gold, effectClassFieldLevel: class}
	classBooks = map[uint32]classBook{}
	for level, row := range classFields {
		if row.ExpRate == 0 {
			return gamedata.Missing("class_upgrade_template", "proficency_to_exp_rant of level %d", level)
		}
		exp := gamedata.IDs(items[row.Item].UsageArg)
		if len(exp) == 0 || exp[0] == 0 {
			return gamedata.Missing("item_data_statistics", "usage_arg of the lecture hall's item %d", row.Item)
		}
		classBooks[row.Item] = classBook{exp: exp[0], maxNum: items[row.Item].MaxNum}
	}
	for name, table := range map[string]map[uint32]resourceField{"oilfield_template": oilFields, "tradingport_template": goldFields} {
		for level, row := range table {
			if row.HourTime == 0 {
				return gamedata.Missing(name, "hour_time of level %d", level)
			}
		}
	}
	return nil
}

// tick is how often the field produces: 20 minutes at hour_time 3, on a
// clock from registration that a harvest doesn't restart, up to store and
// no further while full. Captured: a new account's oil field went from 5 to
// 25 and its merchant from 5 to 65 at register_time + 20 min (SC_11004);
// 25 hours later, never harvested, they held 300 and 900; the merchant
// harvested at :33:14 held 60 at the :38:54 tick.
func (field resourceField) tick() int64 {
	return int64(3600 / field.HourTime)
}

// classTick is how often the lecture hall turns proficiency into exp: on
// the fields' 20-minute ticks, on its own clock from registration. Captured:
// the official server pushes SC_22013 (proficiency, exp_in_well) with the
// fields' SC_11004 at a tick.
const classTick = 20 * 60

// commanderField is one of the commander's two resource fields: the
// player_resource it fills, its configs by level, the effect_args of its
// upgrade, its level, when its running upgrade ends (0 none) and when it
// was last filled (0 never).
type commanderField struct {
	resource  uint32
	configs   map[uint32]resourceField
	effect    string
	level     uint32
	upgradeAt int64
	filledAt  int64
	setAt     func(tx *Tx, at int64) error
}

func commanderFields(commander db.Commander) []commanderField {
	return []commanderField{
		{ResourceOilField, oilFields, effectOilFieldLevel, commander.OilFieldLevel, commander.OilFieldUpgradeAt, commander.OilFieldAt, func(tx *Tx, at int64) error {
			return tx.SetOilFieldAt(tx.ctx, db.SetOilFieldAtParams{OilFieldAt: at, ID: tx.commanderID})
		}},
		{ResourceGoldField, goldFields, effectGoldFieldLevel, commander.GoldFieldLevel, commander.GoldFieldUpgradeAt, commander.GoldFieldAt, func(tx *Tx, at int64) error {
			return tx.SetGoldFieldAt(tx.ctx, db.SetGoldFieldAtParams{GoldFieldAt: at, ID: tx.commanderID})
		}},
	}
}

// fieldLevel is the level of the academy field an upgrade's effect_args
// names and when its running upgrade ends (0 none).
func fieldLevel(commander db.Commander, effect string) (uint32, int64) {
	switch effect {
	case effectOilFieldLevel:
		return commander.OilFieldLevel, commander.OilFieldUpgradeAt
	case effectGoldFieldLevel:
		return commander.GoldFieldLevel, commander.GoldFieldUpgradeAt
	case effectClassFieldLevel:
		return commander.ClassFieldLevel, commander.ClassFieldUpgradeAt
	}
	return 0, 0
}

// setFieldLevel sets the level of the academy field an upgrade's
// effect_args names and when its running upgrade ends (0 none).
func (tx *Tx) setFieldLevel(effect string, level uint32, upgradeAt int64) error {
	switch effect {
	case effectOilFieldLevel:
		return tx.SetOilFieldLevel(tx.ctx, db.SetOilFieldLevelParams{OilFieldLevel: level, OilFieldUpgradeAt: upgradeAt, ID: tx.commanderID})
	case effectGoldFieldLevel:
		return tx.SetGoldFieldLevel(tx.ctx, db.SetGoldFieldLevelParams{GoldFieldLevel: level, GoldFieldUpgradeAt: upgradeAt, ID: tx.commanderID})
	case effectClassFieldLevel:
		return tx.SetClassFieldLevel(tx.ctx, db.SetClassFieldLevelParams{ClassFieldLevel: level, ClassFieldUpgradeAt: upgradeAt, ID: tx.commanderID})
	}
	return errRefused
}

// upgradeEnded is whether a field's running upgrade ended by now: the
// client levels the field up itself when its timer runs out
// (NavalAcademyProxy:UpgradeFinish), without asking.
func upgradeEnded(upgradeAt int64, now time.Time) bool {
	return upgradeAt > 0 && upgradeAt <= now.Unix()
}

// settleResourceFields adds what the oil field and the merchant produced
// since they were last filled, each on its own clock from registration, up
// to their store, and moves their fill time on by the ticks counted. A
// field whose upgrade ended produced at its old level up to the end and
// at the new one after: the client shows the old level's production and
// store until its timer runs out.
func (tx *Tx) settleResourceFields(commander db.Commander, now time.Time) error {
	resources, err := tx.ListResources(tx.ctx, tx.commanderID)
	if err != nil {
		return err
	}
	owned := map[uint32]uint32{}
	for _, row := range resources {
		owned[row.ResourceID] = row.Amount
	}
	for _, field := range commanderFields(commander) {
		since := field.filledAt
		if since == 0 {
			since = commander.RegisteredAt
		}
		filled := since
		if upgradeEnded(field.upgradeAt, now) {
			if filled, err = tx.fillField(field, field.level, filled, field.upgradeAt, owned); err != nil {
				return err
			}
			field.level++
			if err := tx.setFieldLevel(field.effect, field.level, 0); err != nil {
				return err
			}
			if field.effect != effectClassFieldLevel {
				// the manual's canteen/merchant tasks (TaskSubAcademyLevel)
				// count the higher of the two buildings, and only ever go up
				tx.event(TaskEvent{SubType: TaskSubAcademyLevel, Count: field.level})
			}
		}
		if filled, err = tx.fillField(field, field.level, filled, now.Unix(), owned); err != nil {
			return err
		}
		if filled != since {
			if err := field.setAt(tx, filled); err != nil {
				return err
			}
		}
	}
	return tx.settleClassField(commander, owned[ResourceClassField], now)
}

// fillField adds what a field produces at level for every tick from its
// fill time since up to until, up to store, and returns its new fill time.
func (tx *Tx) fillField(field commanderField, level uint32, since, until int64, owned map[uint32]uint32) (int64, error) {
	config, ok := field.configs[level]
	if !ok || config.HourTime == 0 {
		return 0, gamedata.Missing("resource field", "level %d of %d", level, field.resource)
	}
	tick := config.tick()
	ticks := (until - since) / tick
	if ticks <= 0 {
		return since, nil
	}
	room := config.Store - min(owned[field.resource], config.Store)
	if add := min(uint64(ticks)*uint64(config.Production), uint64(room)); add > 0 {
		if err := tx.AddResource(field.resource, uint32(add)); err != nil {
			return 0, err
		}
		owned[field.resource] += uint32(add)
	}
	return since + ticks*tick, nil
}

// classHall is the lecture hall while it is settled: its proficiency, the
// expField it made and when it was last filled.
type classHall struct {
	proficiency, exp uint32
	filledAt         int64
}

// settleClassField turns the lecture hall's proficiency into expField for
// every tick since it was last filled, at its old level up to the end of
// an upgrade that ended and at the new one after, and moves its fill time
// on by the ticks counted.
func (tx *Tx) settleClassField(commander db.Commander, exp uint32, now time.Time) error {
	since := commander.ClassFieldAt
	if since == 0 {
		since = commander.RegisteredAt
	}
	hall := classHall{proficiency: commander.ClassProficiency, exp: exp, filledAt: since}
	level := commander.ClassFieldLevel
	open := commander.Level >= classOpenLevel
	if upgradeEnded(commander.ClassFieldUpgradeAt, now) {
		if err := tx.fillClassField(&hall, level, open, commander.ClassFieldUpgradeAt); err != nil {
			return err
		}
		level++
		if err := tx.setFieldLevel(effectClassFieldLevel, level, 0); err != nil {
			return err
		}
	}
	if err := tx.fillClassField(&hall, level, open, now.Unix()); err != nil {
		return err
	}
	if hall.filledAt == since {
		return nil
	}
	return tx.SetClassField(tx.ctx, db.SetClassFieldParams{
		ClassProficiency: hall.proficiency,
		ClassFieldAt:     hall.filledAt,
		ID:               tx.commanderID,
	})
}

// fillClassField turns proficiency into expField at level for every tick
// from the hall's fill time up to until, proficiency_cost_per_min a
// minute, while there is proficiency and up to stock packs of exp. A hall
// not open yet (classOpenLevel) only moves its clock on: the client locks
// it and its packs below that level.
func (tx *Tx) fillClassField(hall *classHall, level uint32, open bool, until int64) error {
	config, ok := classFields[level]
	if !ok {
		return gamedata.Missing("class_upgrade_template", "level %d", level)
	}
	ticks := (until - hall.filledAt) / classTick
	if ticks <= 0 {
		return nil
	}
	hall.filledAt += ticks * classTick
	if !open {
		return nil
	}
	capacity := uint64(config.Stock) * uint64(classBooks[config.Item].exp)
	room := capacity - min(uint64(hall.exp), capacity)
	spend := min(uint64(ticks)*classTick/60*uint64(config.CostPerMin), uint64(hall.proficiency))
	gain := spend * uint64(config.ExpRate) / 100
	if gain > room {
		gain = room
		spend = gain * 100 / uint64(config.ExpRate)
	}
	if gain > 0 {
		if err := tx.AddResource(ResourceClassField, uint32(gain)); err != nil {
			return err
		}
	}
	hall.proficiency -= uint32(spend)
	hall.exp += uint32(gain)
	return nil
}

// classExtraRate is AcademyCourse:getExtraRate: battles give the lecture
// hall twice the proficiency on the server week's 7th day (Sunday).
func classExtraRate(now time.Time) uint32 {
	if serverWeekday(now) == 7 {
		return 2
	}
	return 1
}

// addClassProficiency is NavalAcademyProxy:AddCourseProficiency for each
// ship exp of a battle that feeds the lecture hall (addShipsExp): it adds
// floor(exp x proficency_get_percent x extra rate x 0.01) to the hall's
// proficiency, up to the level's store, one exp after the other as the
// client does. The hall is settled first, and SC_22013 sends what it then
// holds ahead of the reply: the client adds the same exps to it itself, so
// both end on the same proficiency although belfast doesn't push the ticks.
func (tx *Tx) addClassProficiency(exps []uint32, now time.Time) error {
	if len(exps) == 0 {
		return nil
	}
	commander, err := tx.settleClassForGain(now)
	if err != nil {
		return err
	}
	config := classFields[commander.ClassFieldLevel]
	extra := classExtraRate(now)
	proficiency := commander.ClassProficiency
	for _, exp := range exps {
		// the client's float arithmetic, in its order
		gain := math.Floor(float64(exp) * float64(config.GetPercent) * float64(extra) * 0.01)
		proficiency = uint32(min(float64(proficiency)+gain, float64(config.Store)))
	}
	return tx.SetClassField(tx.ctx, db.SetClassFieldParams{
		ClassProficiency: proficiency,
		ClassFieldAt:     commander.ClassFieldAt,
		ID:               tx.commanderID,
	})
}

// addFlatClassProficiency is NavalAcademyProxy:AddProficiency: amount more
// proficiency in the lecture hall, up to its level's store (an Operational
// Handover's class_exp). The hall is settled and pushed first, as in
// addClassProficiency.
func (tx *Tx) addFlatClassProficiency(amount uint32, now time.Time) error {
	if amount == 0 {
		return nil
	}
	commander, err := tx.settleClassForGain(now)
	if err != nil {
		return err
	}
	return tx.SetClassField(tx.ctx, db.SetClassFieldParams{
		ClassProficiency: min(commander.ClassProficiency+amount, classFields[commander.ClassFieldLevel].Store),
		ClassFieldAt:     commander.ClassFieldAt,
		ID:               tx.commanderID,
	})
}

// settleClassForGain settles the lecture hall before proficiency is added
// and queues SC_22013 with what it then holds, returning the commander as
// settled.
func (tx *Tx) settleClassForGain(now time.Time) (db.Commander, error) {
	expInWell := func() (uint32, error) {
		resources, err := tx.ListResources(tx.ctx, tx.commanderID)
		if err != nil {
			return 0, err
		}
		for _, row := range resources {
			if row.ResourceID == ResourceClassField {
				return row.Amount, nil
			}
		}
		return 0, nil
	}
	commander, err := tx.GetCommander(tx.ctx, tx.commanderID)
	if err != nil {
		return commander, err
	}
	well, err := expInWell()
	if err != nil {
		return commander, err
	}
	if err := tx.settleClassField(commander, well, now); err != nil {
		return commander, err
	}
	if commander, err = tx.GetCommander(tx.ctx, tx.commanderID); err != nil {
		return commander, err
	}
	if well, err = expInWell(); err != nil {
		return commander, err
	}
	tx.push(&protobuf.SC_22013{
		Proficiency: proto.Uint32(commander.ClassProficiency),
		ExpInWell:   proto.Uint32(well),
	})
	return commander, nil
}

// fillResourceFields settles the commander's fields before SC_11003 and
// SC_22001 read them.
func (game *Game) fillResourceFields(ctx context.Context, commander db.Commander, now time.Time) error {
	return game.DB.InTx(ctx, func(queries *db.Queries) error {
		return newTx(ctx, queries, commander.ID).settleResourceFields(commander, now)
	})
}

// handleHarvestResource answers HarvestResourceCommand
// (controller/command/player/harvestresourcecommand.lua): type 1 collects
// the merchant's gold, 2 the oil field's oil, up to the level's max_gold /
// max_oil plus what guild technologies add (Player:getLevelMaxGold). On result 0 the client moves its own count of the field over,
// as much as fits, from the player it read before asking; the server
// settles the field first and moves as much. The official server replies
// only that (captured): it pushes SC_11004 at every tick, so the client's
// count is current. Belfast doesn't push the ticks yet, so it corrects the
// client with SC_11004 after the reply (one sent before would be
// overwritten).
func (game *Game) handleHarvestResource(client *connection.Client, req *protobuf.CS_11013) error {
	var resources []*protobuf.RESOURCE
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		commander, err := tx.GetCommander(tx.ctx, tx.commanderID)
		if err != nil {
			return err
		}
		var resource, field, limit uint32
		var effect string
		switch req.GetType() {
		case ResourceGold:
			resource, field, limit, effect = ResourceGold, ResourceGoldField, commanderLevels[commander.Level].MaxGold, guildTechGoldMax
		case ResourceOil:
			resource, field, limit, effect = ResourceOil, ResourceOilField, commanderLevels[commander.Level].MaxOil, guildTechOilMax
		default:
			return errRefused
		}
		bonus, err := tx.guildTechBonus(effect)
		if err != nil {
			return err
		}
		limit += bonus
		if err := tx.settleResourceFields(commander, time.Now()); err != nil {
			return err
		}
		rows, err := tx.ListResources(tx.ctx, tx.commanderID)
		if err != nil {
			return err
		}
		owned := map[uint32]uint32{}
		for _, row := range rows {
			owned[row.ResourceID] = row.Amount
		}
		if owned[resource] >= limit {
			return errRefused
		}
		take := min(owned[field], limit-owned[resource])
		if err := tx.SpendResource(field, take); err != nil {
			return err
		}
		if err := tx.AddResource(resource, take); err != nil {
			return err
		}
		// SC_11004 replaces every resource (Player:updateResources zeroes
		// the ones it doesn't list), as captured: all of them
		if rows, err = tx.ListResources(tx.ctx, tx.commanderID); err != nil {
			return err
		}
		resources = resourceList(rows)
		return nil
	})
	if err != nil && !refused(err) {
		return err
	}
	if err := client.Send(&protobuf.SC_11014{Result: result(err)}); err != nil || resources == nil {
		return err
	}
	return client.PushAfterReply(&protobuf.SC_11004{ResourceList: resources})
}

// handleHarvestClassResource answers HarvestClassResourceCommand
// (controller/command/player/harvestclassresourcecommand.lua): type 0 packs
// the lecture hall's expField into EXP Data Packs. The client only asks
// when it counts at least one (ClassResourceField:GetCanGetResCnt: whole
// packs of exp, no more than the bag still holds); on result 0 it adds
// that many packs and takes their exp from its expField itself, and sets
// the hall's proficiency to exp_in_well. Never captured: built from the
// Lua. The server settles the hall first and packs as many as it counts,
// none before the hall opens (classOpenLevel: the commission panel's
// bubble is under a lock until then, and the hall makes no exp). Belfast doesn't push the ticks yet (SC_22013), so it corrects the
// client's proficiency and expField with SC_22013 after the reply; a pack
// made by a tick the client didn't see is in the bag at the next login.
func (game *Game) handleHarvestClassResource(client *connection.Client, req *protobuf.CS_22009) error {
	var update *protobuf.SC_22013
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		commander, err := tx.GetCommander(tx.ctx, tx.commanderID)
		if err != nil {
			return err
		}
		if err := tx.settleResourceFields(commander, time.Now()); err != nil {
			return err
		}
		if commander, err = tx.GetCommander(tx.ctx, tx.commanderID); err != nil {
			return err
		}
		if commander.Level < classOpenLevel {
			return errRefused
		}
		config := classFields[commander.ClassFieldLevel]
		book := classBooks[config.Item]
		resources, err := tx.ListResources(tx.ctx, tx.commanderID)
		if err != nil {
			return err
		}
		exp := uint32(0)
		for _, row := range resources {
			if row.ResourceID == ResourceClassField {
				exp = row.Amount
			}
		}
		items, err := tx.ListItems(tx.ctx, tx.commanderID)
		if err != nil {
			return err
		}
		owned := uint32(0)
		for _, row := range items {
			if row.ItemID == config.Item {
				owned = row.Count
			}
		}
		count := min(exp/book.exp, book.maxNum-min(owned, book.maxNum))
		if count == 0 {
			return errRefused
		}
		if err := tx.SpendResource(ResourceClassField, count*book.exp); err != nil {
			return err
		}
		if err := tx.AddItem(config.Item, count); err != nil {
			return err
		}
		update = &protobuf.SC_22013{
			Proficiency: proto.Uint32(commander.ClassProficiency),
			ExpInWell:   proto.Uint32(exp - count*book.exp),
		}
		return nil
	})
	if err != nil && !refused(err) {
		return err
	}
	reply := &protobuf.SC_22010{Result: result(err)}
	if update != nil {
		reply.ExpInWell = proto.Uint32(update.GetProficiency())
	}
	if err := client.Send(reply); err != nil || update == nil {
		return err
	}
	return client.PushAfterReply(update)
}
