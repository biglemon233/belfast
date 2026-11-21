package game

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/ggmolly/belfast/internal/gamedata"
)

// Operation Siren's config tables (mod/experiment/world/model reads them as
// pg.world_*), loaded by loadWorldData.

// worldRandomMap is a world_chapter_random row: a map of the atlas as the
// client knows it (WorldMap.id, the "random id"); the layout it is played
// on is one of its templates, drawn by the server (WORLDMAPID.template_id).
type worldRandomMap struct {
	Name        string   `json:"name"`
	HazardLevel uint32   `json:"hazard_level"` // WorldMap:GetDanger
	OpenStage   []uint32 `json:"open_stage"`   // progress that opens it, by realm
	EnterCost   uint32   `json:"enter_cost"`
	// [template id, weight, lowest progress, highest progress]: the
	// templates it can be played on, drawn by weight among those whose
	// progress range holds the world's progress
	Templates [][4]uint32 `json:"template_id"`
	// 1 for the maps cleared again by the monthly reset (ports, safe zones)
	ResetAutoComplete uint32 `json:"reset_auto_complete"`
	CompleteEffect    uint32 `json:"complete_effect"`
}

// worldCell is a world_chapter_template grids entry: {row, column,
// walkable} (WorldMapCell:Setup).
type worldCell struct {
	Row, Column uint32
	Walkable    bool
}

func (cell *worldCell) UnmarshalJSON(raw []byte) error {
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil || len(parts) < 3 {
		return fmt.Errorf("world cell %s", raw)
	}
	if err := json.Unmarshal(parts[0], &cell.Row); err != nil {
		return err
	}
	if err := json.Unmarshal(parts[1], &cell.Column); err != nil {
		return err
	}
	return json.Unmarshal(parts[2], &cell.Walkable)
}

// worldTemplate is a world_chapter_template row: a map's layout.
type worldTemplate struct {
	Name  string      `json:"name"`
	Grids []worldCell `json:"grids"`
	// {row, column, type, ...} by WorldMapCell terrain type: stream (1)
	// and wind (3) add the direction {row, column} as -1/0/1 and wind its
	// strength, fog (4) and poison (6) a strength, ice (2) nothing
	Terrain [][]int32 `json:"terrain"`
	// [] or [port id, [row, column]]: the port in the map, its cell the
	// center of the 3x3 square WorldMap:SetupPort marks as port
	PortID          json.RawMessage `json:"port_id"`
	Difficulty      uint32          `json:"difficulty"` // gameset world_difficult_value index
	ExpeditionLevel uint32          `json:"expedition_level"`
	IsSairen        uint32          `json:"is_sairen"`
	MapSight        uint32          `json:"map_sight"` // 1: every cell always in sight (WorldMap:AlwaysInFOV)
	IsTransfer      uint32          `json:"is_transfer"`
	IsAuto          uint32          `json:"is_auto"`
	IsClear         uint32          `json:"is_clear"` // leaving the map resets it (WorldMap:NeedClear)
	// InstructionAvailable are the orders the map allows (SVOrderPanel):
	// {submarine support, detection, maintenance}, 0 for not
	InstructionAvailable []uint32 `json:"instruction_available"`

	port       uint32 // port id, 0 for none (or one off the grid)
	portCenter [2]uint32
	walkable   map[[2]uint32]bool // every cell of grids, true when walkable
}

// worldColormask is a world_chapter_colormask row: an entrance of the atlas
// (WorldEntrance, the enter_map_id of a world), drawn over its base map.
type worldColormask struct {
	Chapter     uint32 `json:"chapter"`       // its base map (world_chapter_random)
	PortMapIcon uint32 `json:"port_map_icon"` // its port (world_port_data), 0 for none
	World       uint32 `json:"world"`         // the atlas (WorldConst.DefaultAtlas 1)
	Regions     uint32 `json:"regions"`
	// MapTransfer are the entrances it links to (WorldEntrance.transportDic)
	MapTransfer []uint32 `json:"map_transfer"`
	// CompleteChapter is the base map's secured version (the "Safe Zone"
	// map World:ReplacementMapType shows once the base map is secured)
	CompleteChapter []uint32 `json:"complete_chapter"`
	// TeasureChapter are the treasure maps of the entrance, {coordinates
	// item, map}: a usage_world_map item of the 1000xx range (Map
	// Coordinates) opens an Obscured Zone, one of the 1100xx range (Siren
	// Coordinates, usage_arg [1]) an Abyssal Zone
	TeasureChapter [][2]uint32 `json:"teasure_chapter"`
	// SairenChapter is the entrance's Siren Stronghold map, shown while the
	// entrance is in sairen_chapter (World:EntranceToReplacementMapList)
	SairenChapter []uint32 `json:"sairen_chapter"`
}

// worldZone is what kind of zone a map is, which its content depends on.
type worldZone uint8

const (
	worldZoneOther      worldZone = iota
	worldZoneExplorable           // an entrance's base map with a hazard level
	worldZoneSafe                 // a base map's secured version (complete_chapter)
	worldZoneObscured             // a treasure map of Map Coordinates
	worldZoneAbyssal              // a treasure map of Siren Coordinates
	worldZoneStronghold           // an entrance's Siren Stronghold (sairen_chapter)
)

// worldEvent is a world_event_data row: what an event attachment
// (WorldMapAttachment.TypeEvent) is and where it appears.
type worldEvent struct {
	Name string `json:"name"`
	// the cells it appears on: {row, column} or the rectangle {row1,
	// column1, row2, column2}, one attachment each; negative ones are
	// offsets from the event that spawns it
	Grid         [][]int32 `json:"grid"`
	Effect       []uint32  `json:"effect"`   // world_effect_data, run in order as it is triggered
	EventOp      []uint32  `json:"event_op"` // effects run by the fleet's operations on it
	DiscoverType uint32    `json:"discover_type"`
	IsGuide      uint32    `json:"is_guide"`
	Visuality    uint32    `json:"visuality"`
	Obstacle     uint32    `json:"obstacle"`
	// InfectionValue is the Siren infection the event adds to its map
	// while on it (WorldMap:GetEventPoisonRate)
	InfectionValue uint32 `json:"infection_value"`
	// Icon is what the client draws for the event; empty means it draws
	// nothing (the intact Siren control devices: they can be neither seen
	// nor attacked, but still carry an infection)
	Icon string `json:"icon"`
}

// worldEffect is a world_effect_data row.
type worldEffect struct {
	EffectType uint32          `json:"effect_type"`
	Params     json.RawMessage `json:"effect_paramater"`
	AutoFlag   json.RawMessage `json:"autoflag"`
	// AutoActivate above 0 runs the effect wherever the fleets are
	// (WorldMap:CheckInteractive); EffectiveNum above 1 keeps an event the
	// fleet stands on from being run (the walls' and markers' 410121)
	AutoActivate uint32 `json:"autoactivate"`
	EffectiveNum uint32 `json:"effective_num"`
}

// world_effect_data effect types the server reads (WorldMapAttachment
// lists the client's EffectEvent* ones).
const (
	worldEffectSpawnEvents = 9 // effect_paramater: the event ids to add, one per cell of their grid
)

// worldExpedition is a world_expedition_data row: an enemy of the world
// (its battle is the expedition_data_template of the same id).
type worldExpedition struct {
	MoraleLimit uint32 `json:"morale_limit"` // highest fleet damage level allowed to fight it
	// failed_morale: damage levels a lost battle adds to the fleet
	// (WorldMapFleet:IncDamageLevel)
	FailedMorale uint32 `json:"failed_morale"`
	// exp_world: {difficulty, ship exp} (the template's difficulty)
	ExpWorld [][2]uint32 `json:"exp_world"`
	// award_display_world: {difficulty, [[type, id], ...]}, what the
	// enemy may drop
	AwardDisplayWorld []json.RawMessage `json:"award_display_world"`
	// phase_story: {hp, story} for the bosses whose hp lasts from battle to
	// battle (TypeBoss: the Abyssal Sirens), each hp (1/10000) a reward
	// phase (SC_40004 hp_drop_info)
	PhaseStory []json.RawMessage `json:"phase_story"`
	// PhaseLimit is 1 for the bosses whose lowest hp lasts the month
	// (WorldMapAttachment:IsPeriodEnemy, the Normal Arbiters), 2 for the
	// Hard ones, fought from full hp each time, whose phase rewards are
	// given once a month too
	PhaseLimit uint32 `json:"phase_limit"`
	// PhaseDropDisplay is {hp, [[type, id, count], ...]}: what each hp
	// phase gives (WorldBossHPAwardPanel shows it)
	PhaseDropDisplay []json.RawMessage `json:"phase_drop_display"`
}

// phases are the hp thresholds (1/10000) of a boss's phase_story, highest
// first, nil for an enemy beaten in one battle.
func (expedition worldExpedition) phases() []uint32 {
	var hps []uint32
	for _, raw := range expedition.PhaseStory {
		var entry []json.RawMessage
		if json.Unmarshal(raw, &entry) == nil && len(entry) >= 1 {
			var hp float64
			if json.Unmarshal(entry[0], &hp) == nil && hp >= 0 {
				hps = append(hps, uint32(hp))
			}
		}
	}
	slices.SortFunc(hps, func(a, b uint32) int { return int(b) - int(a) })
	return hps
}

// worldCatSearchNode is a world_catsearch_node row: a find of a
// meowfficer search (CMDCOLLECTION progress_list).
type worldCatSearchNode struct {
	Step        uint32 `json:"step"`  // 1-3, how good it is
	Score       uint32 `json:"score"` // added up into the search's rarity
	SpecialDrop uint32 `json:"special_drop"`
}

// worldPort is a world_port_data row.
type worldPort struct {
	Name     string `json:"name"`
	PortCamp uint32 `json:"port_camp"`
}

// worldStage is a world_stage_template row: what the world's progress
// unlocks (World:IsSystemOpen, BuildFormationIds).
type worldStage struct {
	FleetNum uint32   `json:"fleet_num"`
	StageKey uint32   `json:"stage_key"`
	StageUI  []uint32 `json:"stage_ui"`
}

// boxTemplate is a box_data_template row (WorldMapAttachment.TypeBox).
type boxTemplate struct {
	Name string `json:"name"`
	Type uint32 `json:"type"`
	// EffectID is what the box gives (world_effect_data, a branch on the
	// map's difficulty ending in a drop)
	EffectID uint32 `json:"effect_id"`
}

var (
	worldRandomMaps  map[uint32]worldRandomMap
	worldTemplates   map[uint32]*worldTemplate
	worldColormasks  map[uint32]worldColormask
	worldEvents      map[uint32]worldEvent
	worldEffects     map[uint32]worldEffect
	worldExpeditions map[uint32]worldExpedition
	worldPorts       map[uint32]worldPort
	worldStages      map[uint32]worldStage
	boxTemplates     map[uint32]boxTemplate

	worldCatSearchNodes map[uint32]worldCatSearchNode

	// worldCollectionDrops are the record files' drops: 102000GF is file F
	// (1-6) of the G-th world_collection_file_group (by id), what the
	// "Collection - File" events (100000-100071, 12 folders of 6) give
	worldCollectionDrops map[uint32]uint32

	// worldSpawnedEvents are the events some effect adds to a map
	// (worldEffectSpawnEvents) rather than the map starting with them
	worldSpawnedEvents map[uint32]bool

	// worldZones are the maps' zone kinds, read from the entrances
	// (worldZoneOther for the maps none names)
	worldZones map[uint32]worldZone

	// worldSettings are the gameset values the world reads
	worldSettings struct {
		maxStamina       uint32 // world_movepower_maxvalue
		staminaInterval  uint32 // world_movepower_recovery_interval, seconds per point
		resetStage       uint32 // world_resetting_stage: progress the monthly reset needs
		initialView      uint32 // world_move_initial_view: a fleet's sight radius
		defaultEntrances [][2]uint32
		// world_supply_value / world_supply_price: the n-th oil exchange
		// of the week gives supplyValues[n][0] AP for supplyPrices[n]
		// {type, id, count}
		supplyValues [][]uint32
		supplyPrices [][3]uint32
	}
)

// loadWorldData reads Operation Siren's tables.
func loadWorldData() error {
	var templates map[uint32]worldTemplate
	var gameset map[string]uint32
	var fileGroups map[uint32]struct {
		Child []uint32 `json:"child"`
	}
	err := gamedata.LoadAll(
		gamedata.Load("world_chapter_random", &worldRandomMaps),
		gamedata.Load("world_chapter_template", &templates),
		gamedata.Load("world_chapter_colormask", &worldColormasks),
		gamedata.Load("world_event_data", &worldEvents),
		gamedata.Load("world_effect_data", &worldEffects),
		gamedata.Load("world_expedition_data", &worldExpeditions),
		gamedata.Load("world_port_data", &worldPorts),
		gamedata.Load("world_stage_template", &worldStages),
		gamedata.Load("box_data_template", &boxTemplates),
		gamedata.Load("world_catsearch_node", &worldCatSearchNodes),
		gamedata.Load("world_collection_file_group", &fileGroups),
		func() (err error) {
			gameset, err = gamedata.Gameset()
			return err
		},
		func() error {
			return gamedata.GamesetDescription("world_default_entrance", &worldSettings.defaultEntrances)
		},
		func() error {
			return gamedata.GamesetDescription("world_supply_value", &worldSettings.supplyValues)
		},
		func() error {
			return gamedata.GamesetDescription("world_supply_price", &worldSettings.supplyPrices)
		},
	)
	if err != nil {
		return err
	}
	for key, value := range map[string]*uint32{
		"world_movepower_maxvalue":          &worldSettings.maxStamina,
		"world_movepower_recovery_interval": &worldSettings.staminaInterval,
		"world_resetting_stage":             &worldSettings.resetStage,
		"world_move_initial_view":           &worldSettings.initialView,
	} {
		if *value = gameset[key]; *value == 0 {
			return gamedata.Missing("gameset", "%s", key)
		}
	}
	if len(worldSettings.supplyValues) == 0 || len(worldSettings.supplyValues) != len(worldSettings.supplyPrices) {
		return gamedata.Missing("gameset", "world_supply_value / world_supply_price")
	}
	loadWorldDropSources()
	worldCollectionDrops = map[uint32]uint32{}
	for g, id := range slices.Sorted(maps.Keys(fileGroups)) {
		for f, file := range fileGroups[id].Child[:min(len(fileGroups[id].Child), 9)] {
			worldCollectionDrops[10200000+uint32(g+1)*10+uint32(f+1)] = file
		}
	}
	worldSpawnedEvents = map[uint32]bool{}
	for _, effect := range worldEffects {
		if effect.EffectType != worldEffectSpawnEvents {
			continue
		}
		var ids []uint32
		if json.Unmarshal(effect.Params, &ids) == nil {
			for _, id := range ids {
				worldSpawnedEvents[id] = true
			}
		}
	}
	worldTemplates = make(map[uint32]*worldTemplate, len(templates))
	for id, template := range templates {
		template.walkable = make(map[[2]uint32]bool, len(template.Grids))
		for _, cell := range template.Grids {
			template.walkable[[2]uint32{cell.Row, cell.Column}] = cell.Walkable
		}
		var port []json.RawMessage
		if json.Unmarshal(template.PortID, &port) == nil && len(port) == 2 {
			var center []uint32
			if json.Unmarshal(port[0], &template.port) != nil || json.Unmarshal(port[1], &center) != nil || len(center) != 2 {
				return gamedata.Missing("world_chapter_template", "port_id of %d", id)
			}
			template.portCenter = [2]uint32{center[0], center[1]}
			// most open-sea templates name world_port_data 100 ("Future
			// Content", camp 0) at a cell they don't have: no port there
			if !template.hasCell(center[0], center[1]) {
				template.port = 0
			}
		}
		worldTemplates[id] = &template
	}
	worldZones = map[uint32]worldZone{}
	for _, mask := range worldColormasks {
		if worldRandomMaps[mask.Chapter].HazardLevel > 0 && mask.PortMapIcon == 0 {
			worldZones[mask.Chapter] = worldZoneExplorable
		}
		for _, id := range mask.CompleteChapter {
			worldZones[id] = worldZoneSafe
		}
		for _, id := range mask.SairenChapter {
			worldZones[id] = worldZoneStronghold
		}
		for _, treasure := range mask.TeasureChapter {
			if treasure[0]/10000 == 11 {
				worldZones[treasure[1]] = worldZoneAbyssal
			} else {
				worldZones[treasure[1]] = worldZoneObscured
			}
		}
	}
	// some maps name templates the client doesn't have either (world map
	// 720's 7200): they are never drawn
	for id, row := range worldRandomMaps {
		row.Templates = slices.DeleteFunc(row.Templates, func(entry [4]uint32) bool { return worldTemplates[entry[0]] == nil })
		worldRandomMaps[id] = row
	}
	return nil
}

// isWalkable tells whether the template has a walkable cell at row, column.
func (template *worldTemplate) isWalkable(row, column uint32) bool {
	return template.walkable[[2]uint32{row, column}]
}

// hasCell tells whether row, column is a cell of the template (walkable or
// not), the cells MAPINFO may name.
func (template *worldTemplate) hasCell(row, column uint32) bool {
	_, ok := template.walkable[[2]uint32{row, column}]
	return ok
}

// isPortCell tells whether row, column is one of the cells around the port
// that WorldMap:SetupPort marks as port (a fleet there is in port).
func (template *worldTemplate) isPortCell(row, column uint32) bool {
	if template.port == 0 {
		return false
	}
	dr := int(row) - int(template.portCenter[0])
	dc := int(column) - int(template.portCenter[1])
	return dr >= -1 && dr <= 1 && dc >= -1 && dc <= 1 && (dr != 0 || dc != 0) && template.hasCell(row, column)
}

// worldStageFleets is how many normal fleets the world's progress allows
// (World:BuildFormationIds, at least 2).
func worldStageFleets(progress uint32) uint32 {
	fleets := uint32(2)
	for _, stage := range worldStages {
		if stage.StageKey <= progress {
			fleets = max(fleets, stage.FleetNum)
		}
	}
	return fleets
}

// worldSystemOpen is World:IsSystemOpen for realm 1 (World:GetRealm always
// answers 1): the stage that unlocks a WorldConst.System* is reached.
func worldSystemOpen(system, progress uint32) bool {
	for _, id := range slices.Sorted(maps.Keys(worldStages)) {
		stage := worldStages[id]
		if len(stage.StageUI) == 2 && stage.StageUI[0] == system && (stage.StageUI[1] == 0 || stage.StageUI[1] == 1) {
			return stage.StageKey <= progress
		}
	}
	return true
}
