package game

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// Meowfficers in battle: the cats a sortie's fleets take along
// (GROUPINCHAPTER_P13.commander_list, read by ChapterFleet:updateCommanders),
// the EXP they earn in campaign battles (SC_40004 commander_exp) and what
// their talents change in the rolls the server makes (ambush evasion).

// commander_ability_template add entries: [kind, nations, ship types, attr,
// value] (CommanderTalent:getAttrsAddition)
const (
	talentAdditionNumber = 1 // CommanderConst.TALENT_ADDITION_NUMBER
	talentAdditionRatio  = 2 // CommanderConst.TALENT_ADDITION_RATIO, 1/10000
	// attr of a talent: the index of AttributeType.Dodge in
	// CommanderConst.PROPERTIES
	talentAttrDodge = 9
	// attr of Rising Star (5061, "gains 10.0% more EXP from battles"), next
	// to CommanderConst.DESTROY_ATTR_ID 202 (more EXP as material); the
	// battle one is applied by the server only
	talentAttrBattleExp = 201
)

// talentAddition is one commander_ability_template add entry.
type talentAddition struct {
	kind, attr         uint32
	nations, shipTypes []uint32 // empty: any
	value              float64
}

// mapChapter is what expedition_data_by_map says of a map: its type
// (Map.SCENARIO 1, Map.ELITE 2...) and the chapter number of its title.
type mapChapter struct {
	kind, chapter uint32
}

// meowfficerExpRates are server/meowfficer_battle_exp.json's (wiki-derived,
// see its _source fields): a ship's share by type and rarity, the rate by
// enemy, and the map's bonus by map type as [first chapter, bonus] steps.
type meowfficerExpRates struct {
	shipTypes, rarities map[uint32]float64
	normal, boss        float64
	maps                map[uint32][][2]uint32
	loaded              bool
}

var (
	meowfficerTalentAdds map[uint32][]talentAddition // by talent id
	mapChapters          map[uint32]mapChapter       // by map id
	meowfficerBattleExp  meowfficerExpRates
	// meowfficerDodgeAbility is Commander:getAbilitysAddition's evasion
	// term, in percent: commander_attribute_template has no rate_dodge,
	// so it is (commander_form_a - commander_form_b / commander_form_c) x
	// commander_form_n whatever the cat (0 with the EN gameset)
	meowfficerDodgeAbility float64
)

func loadMeowfficerBattleData() error {
	var talents map[uint32]struct {
		Add [][]json.RawMessage `json:"add"`
	}
	var maps map[uint32]struct {
		Type  uint32          `json:"type"`
		Title json.RawMessage `json:"title"`
	}
	var attributes map[uint32]map[string]any
	err := gamedata.LoadAll(
		gamedata.Load("commander_ability_template", &talents),
		gamedata.Load("expedition_data_by_map", &maps),
		gamedata.Load("commander_attribute_template", &attributes),
		loadMeowfficerBattleExp,
	)
	if err != nil {
		return err
	}
	meowfficerTalentAdds = make(map[uint32][]talentAddition, len(talents))
	for id, row := range talents {
		for _, entry := range row.Add {
			if len(entry) < 5 {
				continue
			}
			var add talentAddition
			if json.Unmarshal(entry[0], &add.kind) != nil || json.Unmarshal(entry[3], &add.attr) != nil || json.Unmarshal(entry[4], &add.value) != nil {
				return fmt.Errorf("commander_ability_template %d: add %s", id, entry)
			}
			add.nations, add.shipTypes = gamedata.IDs(entry[1]), gamedata.IDs(entry[2])
			meowfficerTalentAdds[id] = append(meowfficerTalentAdds[id], add)
		}
	}
	mapChapters = make(map[uint32]mapChapter, len(maps))
	for id, row := range maps {
		chapter := mapChapter{kind: row.Type}
		if title := gamedata.IDs(row.Title); len(title) == 1 {
			chapter.chapter = title[0]
		}
		mapChapters[id] = chapter
	}
	for id, row := range attributes {
		if rate, _ := row["rate_dodge"].(float64); rate != 0 {
			logger.WithFields("Meowfficer", logger.FieldValue("attribute", id)).Warn("commander_attribute_template has a rate_dodge, the ambush evasion leaves it out")
		}
	}
	gameset, err := gamedata.Gameset()
	if err != nil {
		return err
	}
	if gameset["commander_form_c"] == 0 {
		return gamedata.Missing("gameset", "commander_form_c")
	}
	ability := float64(gameset["commander_form_a"]) - float64(gameset["commander_form_b"])/float64(gameset["commander_form_c"])
	meowfficerDodgeAbility = math.Round(ability*float64(gameset["commander_form_n"])*1000) / 1000
	return nil
}

func loadMeowfficerBattleExp() error {
	meowfficerBattleExp = meowfficerExpRates{}
	raw, err := gamedata.ServerFile("meowfficer_battle_exp.json")
	if err != nil {
		return err
	}
	if raw == nil {
		logger.WithFields("Meowfficer").Warn("no server/meowfficer_battle_exp.json: meowfficers get no battle EXP")
		return nil
	}
	var file struct {
		ShipTypes  map[string]any `json:"ship_types"`
		Rarities   map[string]any `json:"rarities"`
		EnemyRates struct {
			Normal float64 `json:"normal"`
			Boss   float64 `json:"boss"`
		} `json:"enemy_rates"`
		Maps map[string]json.RawMessage `json:"maps"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("meowfficer_battle_exp.json: %w", err)
	}
	meowfficerBattleExp = meowfficerExpRates{
		shipTypes: rateTable(file.ShipTypes, numberKey),
		rarities:  rateTable(file.Rarities, numberKey),
		normal:    file.EnemyRates.Normal,
		boss:      file.EnemyRates.Boss,
		maps:      map[uint32][][2]uint32{},
		loaded:    true,
	}
	for key, value := range file.Maps {
		kind, ok := numberKey(key)
		if !ok {
			continue
		}
		var steps [][2]uint32
		if err := json.Unmarshal(value, &steps); err != nil {
			return fmt.Errorf("meowfficer_battle_exp.json maps %s: %w", key, err)
		}
		meowfficerBattleExp.maps[kind] = steps
	}
	return nil
}

// teamMeowfficers is a TEAM_INFO's cats as a fleet's commander list: the
// command cat at pos 1, the support cat at 2.
func teamMeowfficers(team *protobuf.TEAM_INFO) []*protobuf.COMMANDERSINFO {
	var list []*protobuf.COMMANDERSINFO
	for pos, id := range []uint32{team.GetCommanderMain(), team.GetCommanderSub()} {
		if id != 0 {
			list = append(list, &protobuf.COMMANDERSINFO{Pos: proto.Uint32(uint32(pos + 1)), Id: proto.Uint32(id)})
		}
	}
	return list
}

// checkTeamMeowfficers checks the cats of a sortie's or an elite fleet's
// teams as the client picks them (Commander.canEquipToFleetList): owned, in
// one team at most, never two of the same group in a team. With dropUnowned
// a cat no longer owned is taken out (Chapter:EliteCommanderFilter, the
// saved elite fleets may still name a cat used as material) instead of
// refused.
func checkTeamMeowfficers(ctx context.Context, queries *db.Queries, commanderID uint32, teams []*protobuf.TEAM_INFO, dropUnowned bool) error {
	cats, err := ownedMeowfficers(ctx, queries, commanderID)
	if err != nil {
		return err
	}
	seen := map[uint32]bool{}
	for _, team := range teams {
		ids := []*uint32{team.CommanderMain, team.CommanderSub}
		groups := map[uint32]bool{}
		for i, id := range ids {
			if id == nil || *id == 0 {
				continue
			}
			cat, ok := cats[*id]
			if !ok && dropUnowned {
				ids[i] = proto.Uint32(0)
				continue
			}
			group := meowfficerTemplates[cat.TemplateID].GroupType
			if !ok || seen[*id] || groups[group] {
				return errRefused
			}
			seen[*id], groups[group] = true, true
		}
		team.CommanderMain, team.CommanderSub = ids[0], ids[1]
	}
	return nil
}

func ownedMeowfficers(ctx context.Context, queries *db.Queries, commanderID uint32) (map[uint32]db.Meowfficer, error) {
	list, err := queries.ListMeowfficers(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	cats := make(map[uint32]db.Meowfficer, len(list))
	for _, cat := range list {
		cats[cat.ID] = cat
	}
	return cats, nil
}

// groupMeowfficers are the cats a chapter fleet took along that are still
// owned (ChapterFleet:updateCommanders skips the others), by pos.
func groupMeowfficers(ctx context.Context, queries *db.Queries, commanderID uint32, group *protobuf.GROUPINCHAPTER_P13) ([]db.Meowfficer, error) {
	if len(group.GetCommanderList()) == 0 {
		return nil, nil
	}
	cats, err := ownedMeowfficers(ctx, queries, commanderID)
	if err != nil {
		return nil, err
	}
	var list []db.Meowfficer
	for _, slot := range group.GetCommanderList() {
		if cat, ok := cats[slot.GetId()]; ok {
			list = append(list, cat)
		}
	}
	return list, nil
}

// meowfficersInSortie are the cats in the active sortie's fleets: the client
// won't train one of them (UpgradeCommanderCommand, commander_is_in_battle),
// nor feed it as material.
func meowfficersInSortie(ctx context.Context, queries *db.Queries, commanderID uint32) (map[uint32]bool, error) {
	session, err := loadChapter(ctx, queries, commanderID)
	if err != nil || session == nil {
		return nil, err
	}
	busy := map[uint32]bool{}
	for _, groups := range [][]*protobuf.GROUPINCHAPTER_P13{session.info.MainGroupList, session.info.SubmarineGroupList, session.info.SupportGroupList} {
		for _, group := range groups {
			for _, slot := range group.GetCommanderList() {
				busy[slot.GetId()] = true
			}
		}
	}
	return busy, nil
}

// talentBonus sums an attr's additions over a cat's talents for a ship of a
// nationality and type (Commander:getTalentsAddition): a talent counts its
// last matching entry of the kind (getAttrsAddition keeps one per attr).
func talentBonus(cat db.Meowfficer, kind, attr, nationality, shipType uint32) float64 {
	total := 0.0
	for _, talent := range cat.Talents {
		var found *talentAddition
		for i, add := range meowfficerTalentAdds[talent] {
			if add.kind == kind && add.attr == attr {
				found = &meowfficerTalentAdds[talent][i]
			}
		}
		if found == nil {
			continue
		}
		if len(found.nations) > 0 && !slices.Contains(found.nations, nationality) || len(found.shipTypes) > 0 && !slices.Contains(found.shipTypes, shipType) {
			continue
		}
		total += found.value
	}
	return total
}

// attrBonus is what a fleet's cats add to one attribute of a ship.
type attrBonus struct {
	flat, rate float64
}

// meowfficerDodge is what a fleet's cats add to a ship's evasion in
// Ship:getProperties: getAttrValueAddition flat, and getAttrRatioAddition
// / 100 to its rate (the abilities' term, in percent, plus the ratio
// talents in 1/10000).
func meowfficerDodge(cats []db.Meowfficer, ship db.Ship) attrBonus {
	template := ownableShips[ship.TemplateID]
	var bonus attrBonus
	for _, cat := range cats {
		bonus.flat += talentBonus(cat, talentAdditionNumber, talentAttrDodge, template.Nationality, template.Type)
		ratio := talentBonus(cat, talentAdditionRatio, talentAttrDodge, template.Nationality, template.Type)
		bonus.rate += (meowfficerDodgeAbility + ratio/100) / 100
	}
	return bonus
}

// meowfficerShipExp is a ship's share of a battle's meowfficer EXP: its
// type's and its config rarity's, plus its stars.
func meowfficerShipExp(ship db.Ship) float64 {
	template := ownableShips[ship.TemplateID]
	return meowfficerBattleExp.shipTypes[template.Type] + meowfficerBattleExp.rarities[template.Rarity] + float64(template.Star)
}

// meowfficerMapBonus is the bonus of a chapter's map: the last step its
// chapter number reached for its map type, none for the other maps.
func meowfficerMapBonus(template *chapterTemplate) uint32 {
	chapter := mapChapters[template.mapID]
	bonus := uint32(0)
	for _, step := range meowfficerBattleExp.maps[chapter.kind] {
		if chapter.chapter >= step[0] {
			bonus = step[1]
		}
	}
	return bonus
}

// grantBattleMeowfficerExp gives the cats of the fleet that won a campaign
// battle their EXP and returns SC_40004.commander_exp: floor(the ships'
// shares x the enemy's rate) + the map's bonus, the same for both cats,
// plus each cat's Rising Star talents. The client adds it to the cats of
// the chapter fleet (FinishStageCommand.GenerateCommanderExp, an entry per
// cat, 0 when missing) with Commander:addExp, nothing at the last level;
// so does the server (addMeowfficerExp). A lost battle gives none: the
// formula has no rank term and no source says a loss earns EXP. Built from
// the Lua and the wiki: no capture has a meowfficer in a battle.
func (tx *Tx) grantBattleMeowfficerExp(template *chapterTemplate, expedition expeditionTemplate, group *protobuf.GROUPINCHAPTER_P13, ships []db.Ship, multiplier uint32) ([]*protobuf.COMMANDER_EXP, error) {
	if !meowfficerBattleExp.loaded {
		return nil, nil
	}
	cats, err := groupMeowfficers(tx.ctx, tx.Queries, tx.commanderID, group)
	if err != nil || len(cats) == 0 {
		return nil, err
	}
	shares := 0.0
	for _, ship := range ships {
		shares += meowfficerShipExp(ship)
	}
	rate := meowfficerBattleExp.normal
	if expedition.isBoss() {
		rate = meowfficerBattleExp.boss
	}
	// multiplier is what the sortie's operation item doubles (1 without one)
	base := (uint32(calcFloor(shares*rate)) + meowfficerMapBonus(template)) * max(multiplier, 1)
	var list []*protobuf.COMMANDER_EXP
	for _, cat := range cats {
		ratio := talentBonus(cat, talentAdditionRatio, talentAttrBattleExp, 0, 0)
		exp := uint32(calcFloor(float64(base) * (1 + ratio/10000)))
		list = append(list, &protobuf.COMMANDER_EXP{CommanderId: proto.Uint32(cat.ID), Exp: proto.Uint32(exp)})
		level, rest := addMeowfficerExp(cat, exp)
		if level == cat.Level && rest == cat.Exp {
			continue
		}
		if err := tx.SetMeowfficerLevel(tx.ctx, db.SetMeowfficerLevelParams{Level: level, Exp: rest, CommanderID: tx.commanderID, ID: cat.ID}); err != nil {
			return nil, err
		}
	}
	return list, nil
}
