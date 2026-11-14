package game

import (
	"encoding/json"
	"fmt"

	"github.com/ggmolly/belfast/internal/gamedata"
)

// The campaign's game data, built from the config tables chapter_template,
// expedition_data_template, item_virtual_data_statistics, ship_level,
// energy_template and gameset.

// chapter_template.grids cell kinds (the 4th value of a grid)
const (
	gridBorn  = 1
	gridElite = 4 // an elite spawns here (elite_expedition_list)
	gridEnemy = 6 // an enemy spawns here (expedition_id_weight_list)
	gridBoss  = 8 // the boss spawns here (boss_expedition_id)
	// gridSubBorn is where a submarine fleet starts (ChapterConst.AttachBorn_Sub)
	gridSubBorn = 16
)

type chapterGrid struct {
	row, column uint32
	walkable    bool
	kind        uint32
}

type weightedExpedition struct {
	id, weight uint32
}

type chapterWinCondition struct {
	kind, param uint32
}

// ambushRatio is a chapter_template.ambush_ratio_extra row, ratio in 1/10000;
// a row without a cell applies everywhere.
type ambushRatio struct {
	row, column uint32
	anyCell     bool
	ratio       int
}

type chapterTemplate struct {
	id        uint32
	mapID     uint32 // expedition_data_by_map
	oil       uint32 // oil spent to sortie
	time      uint32 // seconds the sortie stays open
	ammoTotal uint32
	// ammoSubmarine is what a submarine fleet's torpedo tubes fire per sortie
	// (chapter_template.ammo_submarine, ChapterLevelData's submarine bullets)
	ammoSubmarine uint32
	progressBoss  uint32 // progress gained per boss kill
	grids         []chapterGrid
	enemies       []weightedExpedition // expedition_id_weight_list
	// enemies / elites per wave (enemy_refresh, elite_refresh), the first
	// wave at start, see chapterSession.spawnWave
	enemyRefresh  []int
	eliteRefresh  []int
	elites        []uint32 // elite_expedition_list
	bosses        []uint32 // boss_expedition_id
	bossRefresh   uint32   // kills before the boss appears
	isAmbush      bool
	ambushes      []uint32 // ambush_expedition_list
	ambushRatios  []ambushRatio
	avoidRatio    uint32 // how hard ambushes are to evade
	winConditions []chapterWinCondition
	// award [type, id]: a virtual item id (drawn from its dropPools) or, when
	// there is no such pool, the item itself — hard mode chapters list their
	// core data and equipment directly
	awards [][2]uint32

	// how the fleets are picked (chapter_template.type): chapterSelectFleet
	// or chapterCustomFleet
	fleetType uint32
	// hard is a hard mode chapter (困难海域): its map is the 200s, one per
	// normal chapter's map (10101 is 1-1's hard map 201). The client lists
	// them separately and the commander manual's daily 7205 counts them.
	hard bool
	// coreData is what a win of a hard chapter pays (items.go itemCoreData),
	// the chapter's amount from the wiki's 核心兑换 table
	coreData uint32
	// the elite fleet set the chapter shares with the others of its
	// formation (ChapterProxy.mapEliteFleetCache, SC_13001 fleet_list)
	formation uint32
	// per surface fleet, the main then the vanguard slot limits
	limitation                         [][2][]shipLimit
	groupNum, submarineNum, supportNum uint32 // group_num, submarine_num, support_group_num
	// misc_arg submarine_support: the support fleet is submarines, not carriers
	submarineSupport bool
}

var chapterTemplates map[uint32]*chapterTemplate

// chapter_template row
type chapterConfig struct {
	Map           uint32 `json:"map"`
	Oil           uint32 `json:"oil"`
	Time          uint32 `json:"time"`
	AmmoTotal     uint32 `json:"ammo_total"`
	AmmoSubmarine uint32 `json:"ammo_submarine"`
	ProgressBoss  uint32 `json:"progress_boss"`
	// [row, column, walkable, kind], walkable a boolean or 0/1
	Grids                  [][4]any   `json:"grids"`
	ExpeditionIDWeightList [][]uint32 `json:"expedition_id_weight_list"`
	EnemyRefresh           []int      `json:"enemy_refresh"`
	EliteRefresh           []int      `json:"elite_refresh"`
	EliteExpeditionList    []uint32   `json:"elite_expedition_list"`
	BossExpeditionID       []uint32   `json:"boss_expedition_id"`
	BossRefresh            uint32     `json:"boss_refresh"`
	IsAmbush               uint32     `json:"is_ambush"`
	AmbushExpeditionList   []uint32   `json:"ambush_expedition_list"`
	AmbushRatioExtra       [][]int    `json:"ambush_ratio_extra"`
	AvoidRatio             uint32     `json:"avoid_ratio"`
	// [kind, param], param a cell for the kinds that name one
	WinCondition [][2]any    `json:"win_condition"`
	Awards       [][2]uint32 `json:"awards"` // [type, virtual item]
	Type         uint32      `json:"type"`
	Formation    uint32      `json:"formation"`
	// per fleet [main slots, vanguard slots], a slot a ship type, a
	// ShipType.BundleList name or 0 (any)
	Limitation      [][2][]any `json:"limitation"`
	GroupNum        uint32     `json:"group_num"`
	SubmarineNum    uint32     `json:"submarine_num"`
	SupportGroupNum uint32     `json:"support_group_num"`
	MiscArg         [][]any    `json:"misc_arg"` // [key, value]
}

// expedition_data_template.type
const (
	expeditionTypeMulBoss = 94
	expeditionTypeBoss    = 99
)

// expeditionTemplate is an expedition_data_template row.
type expeditionTemplate struct {
	Exp   uint32 `json:"exp"`   // base ship exp
	Level uint32 `json:"level"` // the enemy's level
	Type  uint32 `json:"type"`
	// award_display is this enemy's own drop list: the client shows it as
	// the battle's "possible drops", and it is where a normal battle's
	// items come from (the chapter's awards are the boss's ship table).
	// Some entries' counts are localised words ("少量", "46~50"), so the
	// list is read leniently (numeric drops): the row still loads for its
	// exp and level.
	AwardDisplay json.RawMessage `json:"award_display"`

	awards [][]uint32
}

var expeditionTemplates map[uint32]expeditionTemplate

// numericDrops keeps the [type, id] or [type, id, count] entries of an award
// list: the client tables also give some counts as words ("少量", "46~50"),
// which the server has no amount for and leaves out.
func numericDrops(raw json.RawMessage) [][]uint32 {
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	var drops [][]uint32
	for _, entry := range entries {
		var drop []uint32
		if json.Unmarshal(entry, &drop) == nil && len(drop) >= 2 {
			drops = append(drops, drop)
		}
	}
	return drops
}

func (expedition expeditionTemplate) isBoss() bool {
	return expedition.Type == expeditionTypeBoss || expedition.Type == expeditionTypeMulBoss
}

type dropRef struct {
	kind, id uint32
}

// dropPools are the "chance to receive" award items of
// item_virtual_data_statistics, display_icon listing what they can turn into.
var dropPools map[uint32][]dropRef

// item_virtual_data_statistics row: display_icon is a list of
// [type, id, count], or "" when there is none
type virtualItemConfig struct {
	DisplayIcon json.RawMessage `json:"display_icon"`
	LinkID      uint32          `json:"link_id"`
	// Name is the placeholder's name ("未知教材T4"): its tier is what the
	// contents the client data leaves out are drawn from
	Name string `json:"name"`
}

// itemNameType is what item_data_statistics says about an item, the fields
// deriveVirtualPools reads.
type itemNameType struct {
	Type    uint32 `json:"type"`
	Display string `json:"display"`
	Rarity  uint32 `json:"rarity"`
}

// Specialized Core: virtual item 59011 (virtual_type 20) is stored as the
// item that is its link_id, capped by gameset urpt_chapter_max.
const vitemSpecializedCore = 59011

var itemSpecializedCore uint32

// gameset values
var (
	flagshipExpPercent  uint32 // flagship_exp_percent
	mvpExpPercent       uint32 // mvp_exp_percent
	battleConsumeEnergy uint32 // battle_consume_energy
	expOverflowMax      uint32 // exp_overflow_max
	levelGetProficency  uint32 // level_get_proficency
)

// gamesetValues are the gameset settings loadChapterData reads.
var gamesetValues = map[string]*uint32{
	"flagship_exp_percent":  &flagshipExpPercent,
	"mvp_exp_percent":       &mvpExpPercent,
	"battle_consume_energy": &battleConsumeEnergy,
	"exp_overflow_max":      &expOverflowMax,
	"level_get_proficency":  &levelGetProficency,
}

// energyHappy is the energy_template row of the highest morale, which
// gives an exp bonus.
const energyHappy = 4

// energy_template row
type energyConfig struct {
	LowerBound uint32 `json:"lower_bound"`
	UpperBound uint32 `json:"upper_bound"`
}

// energyStates are the morale brackets (energy_template) by id.
var energyStates map[uint32]energyConfig

// shipLevelExp is ship_level exp by level: shipLevelExp[n-1] takes a ship
// from level n to n+1.
var shipLevelExp []uint32

// shipFightOilRatio is ship_level fight_oil_ratio by level, as shipLevelExp.
var shipFightOilRatio []uint32

// ship_level row
type shipLevelConfig struct {
	Level         uint32 `json:"level"`
	Exp           uint32 `json:"exp"`
	ExpUR         uint32 `json:"exp_ur"`
	FightOilRatio uint32 `json:"fight_oil_ratio"` // oil_at_end x this / 10000
}

// itemNameTypes is item_data_statistics, the items a drop list can name
// directly (as opposed to the virtual placeholders dropPools holds).
var itemNameTypes map[uint32]itemNameType

// loadChapterData builds every chapter of chapter_template, the expeditions
// fought in them, the award pools and the ship level curve.
func loadChapterData() error {
	var chapters map[uint32]chapterConfig
	var items map[uint32]virtualItemConfig
	itemStats := map[uint32]itemNameType{}
	var levels map[uint32]shipLevelConfig
	var gameset map[string]uint32
	err := gamedata.LoadAll(
		gamedata.Load("chapter_template", &chapters),
		gamedata.Load("expedition_data_template", &expeditionTemplates),
		gamedata.Load("item_virtual_data_statistics", &items),
		gamedata.Load("item_data_statistics", &itemStats),
		gamedata.Load("ship_level", &levels),
		gamedata.Load("energy_template", &energyStates),
		func() (err error) {
			gameset, err = gamedata.Gameset()
			return err
		},
	)
	if err != nil {
		return err
	}
	for id, expedition := range expeditionTemplates {
		expedition.awards = numericDrops(expedition.AwardDisplay)
		expeditionTemplates[id] = expedition
	}
	for key, value := range gamesetValues {
		setting, ok := gameset[key]
		if !ok {
			return gamedata.Missing("gameset", "%s", key)
		}
		*value = setting
	}
	if _, ok := energyStates[energyHappy]; !ok {
		return gamedata.Missing("energy_template", "row %d", energyHappy)
	}
	if itemSpecializedCore = items[vitemSpecializedCore].LinkID; itemSpecializedCore == 0 {
		return gamedata.Missing("item_virtual_data_statistics", "link_id for %d", vitemSpecializedCore)
	}
	chapterTemplates = make(map[uint32]*chapterTemplate, len(chapters))
	for id, row := range chapters {
		template := &chapterTemplate{
			id:            id,
			mapID:         row.Map,
			oil:           row.Oil,
			time:          row.Time,
			ammoTotal:     row.AmmoTotal,
			ammoSubmarine: row.AmmoSubmarine,
			progressBoss:  row.ProgressBoss,
			bosses:        row.BossExpeditionID,
			bossRefresh:   row.BossRefresh,
			isAmbush:      row.IsAmbush != 0,
			ambushes:      row.AmbushExpeditionList,
			avoidRatio:    row.AvoidRatio,
		}
		for _, grid := range row.Grids {
			cellRow, _ := grid[0].(float64)
			cellColumn, _ := grid[1].(float64)
			kind, _ := grid[3].(float64)
			walkable, ok := grid[2].(bool)
			if !ok {
				number, _ := grid[2].(float64)
				walkable = number != 0
			}
			template.grids = append(template.grids, chapterGrid{uint32(cellRow), uint32(cellColumn), walkable, uint32(kind)})
		}
		for _, entry := range row.ExpeditionIDWeightList {
			if len(entry) >= 2 {
				template.enemies = append(template.enemies, weightedExpedition{entry[0], entry[1]})
			}
		}
		template.enemyRefresh, template.eliteRefresh, template.elites = row.EnemyRefresh, row.EliteRefresh, row.EliteExpeditionList
		for _, extra := range row.AmbushRatioExtra {
			switch len(extra) {
			case 1:
				template.ambushRatios = append(template.ambushRatios, ambushRatio{anyCell: true, ratio: extra[0]})
			case 3:
				template.ambushRatios = append(template.ambushRatios, ambushRatio{row: uint32(extra[0]), column: uint32(extra[1]), ratio: extra[2]})
			}
		}
		for _, condition := range row.WinCondition {
			kind, _ := condition[0].(float64)
			param, _ := condition[1].(float64)
			template.winConditions = append(template.winConditions, chapterWinCondition{uint32(kind), uint32(param)})
		}
		for _, award := range row.Awards {
			template.awards = append(template.awards, award)
		}
		template.fleetType, template.formation = row.Type, row.Formation
		// hard mode chapters share the normal chapters' ids times 100 and
		// live on the maps after 200 (10101 is 1-1's hard map 201); every
		// win pays the chapter's core data (wiki 核心兑换: chapter 1 gives 4,
		// 2 gives 8, then 2 more per chapter, up to 32 at 14-4)
		template.hard = row.Map >= 200 && row.Map < 300
		if chapter := row.Map - 200; template.hard {
			switch {
			case chapter <= 1:
				template.coreData = 4
			case chapter == 2:
				template.coreData = 8
			default:
				template.coreData = 2*chapter + 4
			}
		}
		template.groupNum, template.submarineNum, template.supportNum = row.GroupNum, row.SubmarineNum, row.SupportGroupNum
		for _, fleet := range row.Limitation {
			var limits [2][]shipLimit
			for half, slots := range fleet {
				for _, slot := range slots {
					limit, err := parseShipLimit(slot)
					if err != nil {
						return fmt.Errorf("chapter_template %d limitation: %w", id, err)
					}
					limits[half] = append(limits[half], limit)
				}
			}
			template.limitation = append(template.limitation, limits)
		}
		for _, arg := range row.MiscArg {
			if len(arg) == 2 && arg[0] == "submarine_support" {
				template.submarineSupport = arg[1] != nil // a ship template id
			}
		}
		chapterTemplates[id] = template
	}

	dropPools = map[uint32][]dropRef{}
	for id, row := range items {
		var icons [][]uint32
		if json.Unmarshal(row.DisplayIcon, &icons) != nil {
			continue
		}
		for _, icon := range icons {
			if len(icon) >= 2 {
				dropPools[id] = append(dropPools[id], dropRef{icon[0], icon[1]})
			}
		}
	}
	deriveVirtualPools(items, itemStats)
	itemNameTypes = itemStats

	maxLevel := uint32(0)
	for _, row := range levels {
		maxLevel = max(maxLevel, row.Level)
	}
	shipLevelExp = make([]uint32, maxLevel)
	shipLevelExpUR = make([]uint32, maxLevel)
	shipFightOilRatio = make([]uint32, maxLevel)
	for _, row := range levels {
		if row.Level > 0 {
			shipLevelExp[row.Level-1] = row.Exp
			shipLevelExpUR[row.Level-1] = row.ExpUR
			shipFightOilRatio[row.Level-1] = row.FightOilRatio
		}
	}
	return nil
}
