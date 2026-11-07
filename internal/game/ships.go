package game

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/protobuf"
)

const (
	// every ship is obtained at 50.00 affinity (captured SHIPINFO.intimacy)
	newShipIntimacy = 5000
	// a SHIPINFO always lists 5 equipment slots (ship_data_template equip_1..5),
	// empty ones with id 0
	shipEquipSlots = 5
	// Ship.ENERGY_RECOVER_TIME, the morale tick
	energyRecoverTime = 360
)

// NewShip returns the row for a freshly obtained ship: level 1, morale and
// level cap from its own ship_data_template (energy, max_level), which every
// template has (a limit broken or 9000xx variant has its own max_level).
func NewShip(commanderID uint32, templateID uint32) db.CreateShipParams {
	// every base template has energy 150 and max_level 70
	params := db.CreateShipParams{
		CommanderID: commanderID,
		TemplateID:  templateID,
		Level:       1,
		Energy:      150,
		Intimacy:    newShipIntimacy,
		MaxLevel:    70,
		CreatedAt:   time.Now().Unix(),
	}
	if template, ok := ownableShips[templateID]; ok {
		params.Energy = template.Energy
		params.MaxLevel = template.MaxLevel
	}
	return params
}

// AddShip gives the commander a new ship of templateID, any ownableShip (not
// only a base template), with the equipment (its ship_data_template
// equip_id_1..3) and skills (its buff_list, level 1) it comes with, and
// records its group in the archives. When that unlocks a new archive entry
// its SC_17002 is pushed (the official server sends it before the reply or
// push carrying the ship).
func (tx *Tx) AddShip(templateID uint32) (*protobuf.SHIPINFO, error) {
	ctx, queries := tx.ctx, tx.Queries
	template, ok := ownableShip(templateID)
	if !ok {
		return nil, fmt.Errorf("ship template %d: %w", templateID, errRefused)
	}
	row, err := queries.CreateShip(ctx, NewShip(tx.commanderID, templateID))
	if err != nil {
		return nil, err
	}
	equips := make([]db.ShipEquipment, shipEquipSlots)
	for i := range equips {
		equips[i] = db.ShipEquipment{ShipID: row.ID, Pos: uint32(i + 1)}
		if given := template.equips(); i < len(given) {
			equips[i].EquipID = given[i]
		}
		if err := queries.SetShipEquipment(ctx, db.SetShipEquipmentParams{ShipID: row.ID, Pos: equips[i].Pos, EquipID: equips[i].EquipID}); err != nil {
			return nil, err
		}
	}
	skills := make([]db.ShipSkill, len(template.Skills))
	for i, skill := range template.Skills {
		skills[i] = db.ShipSkill{ShipID: row.ID, SkillID: skill, Level: 1}
		if err := queries.AddShipSkill(ctx, db.AddShipSkillParams{ShipID: row.ID, SkillID: skill, Level: 1}); err != nil {
			return nil, err
		}
	}
	push, err := recordShipGroup(ctx, queries, row)
	if err != nil {
		return nil, err
	}
	if push != nil {
		tx.push(push)
		tx.techPointsChanged()
	} else if raised, err := raiseShipGroup(ctx, queries, row); err != nil {
		return nil, err
	} else if raised {
		// a limit broken copy of a group already there
		tx.techPointsChanged()
	}
	return fullShipInfo(row, equips, skills), nil
}

// shipInfo is the SHIPINFO of a ship row alone, read by BayProxy into a Ship
// (model/vo/ship.lua).
func shipInfo(ship db.Ship) *protobuf.SHIPINFO {
	locked := uint32(0)
	if ship.IsLocked {
		locked = 1
	}
	info := &protobuf.SHIPINFO{
		Id:                  proto.Uint32(ship.ID),
		TemplateId:          proto.Uint32(ship.TemplateID),
		Level:               proto.Uint32(ship.Level),
		Exp:                 proto.Uint32(ship.Exp),
		Energy:              proto.Uint32(ship.Energy),
		State:               proto.Uint32(ship.State),
		IsLocked:            proto.Uint32(locked),
		Intimacy:            proto.Uint32(ship.Intimacy),
		Proficiency:         proto.Uint32(ship.Proficiency),
		CreateTime:          proto.Uint32(uint32(ship.CreatedAt)),
		SkinId:              proto.Uint32(ship.SkinID),
		Propose:             proto.Uint32(ship.Propose),
		Name:                proto.String(ship.Name),
		ChangeNameTimestamp: proto.Uint32(ship.ChangeNameTimestamp),
		MaxLevel:            proto.Uint32(ship.MaxLevel),
		CommonFlag:          proto.Uint32(ship.CommonFlag),
	}
	if ship.RandomFlag {
		info.CharRandomFlag = []uint32{0} // the phantoms in the random pool, 0 the ship itself
	}
	return info
}

// fullShipInfo is the SHIPINFO of a ship with its slots and skills.
func fullShipInfo(ship db.Ship, equips []db.ShipEquipment, skills []db.ShipSkill) *protobuf.SHIPINFO {
	info := shipInfo(ship)
	info.EquipInfoList = make([]*protobuf.EQUIPSKIN_INFO, shipEquipSlots)
	for i := range info.EquipInfoList {
		info.EquipInfoList[i] = &protobuf.EQUIPSKIN_INFO{}
	}
	for _, equip := range equips {
		if equip.Pos >= 1 && equip.Pos <= shipEquipSlots {
			info.EquipInfoList[equip.Pos-1] = &protobuf.EQUIPSKIN_INFO{Id: proto.Uint32(equip.EquipID), SkinId: proto.Uint32(equip.SkinID)}
		}
	}
	for _, skill := range skills {
		info.SkillIdList = append(info.SkillIdList, &protobuf.SHIPSKILL{
			SkillId: proto.Uint32(skill.SkillID), SkillLv: proto.Uint32(skill.Level), SkillExp: proto.Uint32(skill.Exp),
		})
	}
	return info
}

// shipList is SC_12001, BayProxy's ships: the SHIPINFO of each of ships
// (the commander's, by id) with their slots, skills, enhancements,
// retrofits, augments, unlocked skin shadows and META repairs.
func shipList(ctx context.Context, queries *db.Queries, commanderID uint32, ships []db.Ship) (*protobuf.SC_12001, error) {
	equips, err := queries.ListShipEquipmentsByCommander(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	skills, err := queries.ListShipSkillsByCommander(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	strengths, err := queries.ListShipStrengthsByCommander(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	transformRows, err := queries.ListShipTransformsByCommander(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	transformsByShip := map[uint32][]db.ShipTransform{}
	for _, row := range transformRows {
		transformsByShip[row.ShipID] = append(transformsByShip[row.ShipID], row)
	}
	strengthsByShip := map[uint32][]db.ShipStrength{}
	for _, row := range strengths {
		strengthsByShip[row.ShipID] = append(strengthsByShip[row.ShipID], row)
	}
	equipsByShip := map[uint32][]db.ShipEquipment{}
	for _, equip := range equips {
		equipsByShip[equip.ShipID] = append(equipsByShip[equip.ShipID], equip)
	}
	skillsByShip := map[uint32][]db.ShipSkill{}
	for _, skill := range skills {
		skillsByShip[skill.ShipID] = append(skillsByShip[skill.ShipID], skill)
	}
	shadows, err := queries.ListShipSkinShadowsByCommander(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	shadowsByShip := map[uint32][]db.ShipSkinShadow{}
	for _, shadow := range shadows {
		shadowsByShip[shadow.ShipID] = append(shadowsByShip[shadow.ShipID], shadow)
	}
	msg := &protobuf.SC_12001{Shiplist: make([]*protobuf.SHIPINFO, len(ships))}
	for i, ship := range ships {
		msg.Shiplist[i] = fullShipInfo(ship, equipsByShip[ship.ID], skillsByShip[ship.ID])
		msg.Shiplist[i].StrengthList = strengthInfo(strengthsByShip[ship.ID])
		msg.Shiplist[i].TransformList = transformInfo(transformsByShip[ship.ID])
		msg.Shiplist[i].SkinShadowList = skinShadowList(shadowsByShip[ship.ID])
	}
	if err := setShipSpWeapons(ctx, queries, commanderID, msg.Shiplist); err != nil {
		return nil, err
	}
	if err := setMetaRepairLists(ctx, queries, commanderID, msg.Shiplist); err != nil {
		return nil, err
	}
	return msg, nil
}

// shipEquipments is the 5 slots of one of the commander's ships,
// errRefused if it isn't theirs.
func shipEquipments(ctx context.Context, queries *db.Queries, commanderID, shipID uint32) ([]db.ShipEquipment, error) {
	_, err := queries.GetShip(ctx, db.GetShipParams{ID: shipID, CommanderID: commanderID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errRefused
	} else if err != nil {
		return nil, err
	}
	rows, err := queries.ListShipEquipments(ctx, shipID)
	if err != nil {
		return nil, err
	}
	slots := make([]db.ShipEquipment, shipEquipSlots)
	for i := range slots {
		slots[i] = db.ShipEquipment{ShipID: shipID, Pos: uint32(i + 1)}
	}
	for _, row := range rows {
		if row.Pos >= 1 && row.Pos <= shipEquipSlots {
			slots[row.Pos-1] = row
		}
	}
	return slots, nil
}

// ownsShips tells whether every ship of ids is the commander's.
func ownsShips(ctx context.Context, queries *db.Queries, commanderID uint32, ids []uint32) (bool, error) {
	distinct := slices.Compact(slices.Sorted(slices.Values(ids)))
	if len(distinct) == 0 {
		return true, nil
	}
	owned, err := queries.CountOwnedShips(ctx, db.CountOwnedShipsParams{CommanderID: commanderID, Ids: distinct})
	return owned == int64(len(distinct)), err
}

// shipsByID loads the ships of ids that are the commander's, by id.
func shipsByID(ctx context.Context, queries *db.Queries, commanderID uint32, ids []uint32) (map[uint32]db.Ship, error) {
	rows, err := queries.ListShipsByID(ctx, db.ListShipsByIDParams{CommanderID: commanderID, Ids: slices.Compact(slices.Sorted(slices.Values(ids)))})
	if err != nil {
		return nil, err
	}
	ships := make(map[uint32]db.Ship, len(rows))
	for _, ship := range rows {
		ships[ship.ID] = ship
	}
	return ships, nil
}

// calcFloor is the client's floor (support/helpers/mathssupport.lua).
func calcFloor(value float64) float64 {
	return math.Floor(value + 1e-9)
}

// intimacyBonus is intimacy_template[Ship:getIntimacyLevel()].attr_bonus
// (1/10000): the row whose range holds the affinity, at least the oath's.
// The client caps an unoathed ship's affinity at 100.00 (Ship:Ctor).
func intimacyBonus(intimacy uint32, propose bool) uint32 {
	if !propose {
		intimacy = min(intimacy, maxIntimacy)
	}
	level := 0
	for i, row := range intimacies {
		if row.LowerBound <= intimacy && intimacy <= row.UpperBound {
			level = i + 1
			break
		}
	}
	if propose && level < intimacyPropose {
		level = intimacyPropose
	}
	if level == 0 {
		return 0
	}
	return intimacies[level-1].AttrBonus
}

// activeEquipments is Ship:getActiveEquipments: the equipment of slots (by
// position, 0 when empty), a piece dropped (0) when an earlier slot holds
// one with the same non-zero equip_limit.
func activeEquipments(slots []uint32) []uint32 {
	active := slices.Clone(slots)
	for i := len(active) - 1; i >= 0; i-- {
		limit := equipEvasions[active[i]].limit
		if active[i] == 0 || limit == 0 {
			continue
		}
		for _, earlier := range active[:i] {
			if earlier != 0 && equipEvasions[earlier].limit == limit {
				active[i] = 0
				break
			}
		}
	}
	return active
}

// shipDodge is a ship's evasion as Ship:getProperties (model/vo/ship.lua)
// computes it from what belfast models, its equipment by slot:
//   - getBaseProperties / getGrowthForAttr: attrs plus attrs_growth per
//     level, plus attrs_growth_extra above extra_attr_level_limit (the
//     "lock" loop copies the same values, it changes nothing);
//   - getShipProperties: evasion isn't enhanceable (ShipModAttr.ATTR_TO_INDEX),
//     then intimacyAdditions, then the dodge of its retrofits' effects
//     (transforms); blueprint and META ships aren't modeled and add nothing;
//   - getEquipmentProperties: the flat dodge of the active equipment and the
//     best dodge property_rate (augments aren't modeled);
//   - getProperties: floor(floor(base) * (1 + rate + the cats' rate)) +
//     flat + the cats' flat (meowfficerDodge). The fleet tech nation bonus
//     of max star ships and Operation Siren buffs aren't modeled and add
//     nothing.
func shipDodge(ship db.Ship, slots []uint32, retrofits []db.ShipTransform, cats attrBonus) float64 {
	growth := shipDodges[ship.TemplateID]
	dodge := growth.base + (float64(ship.Level)-1)*growth.growth/1000
	if ship.Level > extraAttrLevelLimit {
		dodge += float64(ship.Level-extraAttrLevelLimit) * growth.growthExtra / 1000
	}
	dodge *= 1 + float64(intimacyBonus(ship.Intimacy, ship.Propose != 0))/10000
	dodge += transformBonus(retrofits, attributeDodge)
	flat, rate := 0.0, 0.0
	for _, equip := range activeEquipments(slots) {
		evasion := equipEvasions[equip]
		flat += evasion.dodge
		rate = max(rate, evasion.dodgeRate)
	}
	return calcFloor(calcFloor(dodge)*(1+rate+cats.rate)) + flat + cats.flat
}

// grantShipsStarter gives a new commander the starter ship they chose and
// the ships of new_commander.json (Long Island), equipped, and the starting
// bag (SC_12001 / SC_15001 of the capture), and sets the medals that start
// with progress.
func grantShipsStarter(tx *Tx, commander db.Commander) error {
	ctx, queries, commanderID := tx.ctx, tx.Queries, tx.commanderID
	for _, template := range append([]uint32{commander.StarterShip}, newCommander.Ships...) {
		if _, err := tx.AddShip(template); err != nil {
			return err
		}
	}
	if err := grantStarterItems(tx); err != nil {
		return err
	}
	// Renovation Merit Medal (medal_template 10041, decor tokens spent) counts
	// the starter backyard as bought (captured 14)
	if err := queries.SetTrophy(ctx, db.SetTrophyParams{CommanderID: commanderID, TrophyID: 10041, Progress: starterFurnitureTokens}); err != nil {
		return err
	}
	return setShipTrophies(ctx, queries, commanderID)
}

// energyTick is SC_12031, the last morale tick: they run every
// Ship.ENERGY_RECOVER_TIME seconds since the commander was created (the
// capture's value is the creation time).
func energyTick(commander db.Commander, now int64) *protobuf.SC_12031 {
	last := commander.RegisteredAt
	if now > last {
		last += (now - last) / energyRecoverTime * energyRecoverTime
	}
	return &protobuf.SC_12031{EnergyAutoIncreaseTime: proto.Uint32(uint32(last))}
}

// restoreMorale refills the morale of every ship below the cap when the
// commander logs in. The client only counts the morale tick itself (SC_12031)
// and never writes the recovered morale back, so without this a ship that
// spent its morale never recovers it.
func restoreMorale(ctx context.Context, queries *db.Queries, commanderID uint32) error {
	ships, err := queries.ListShipsByCommander(ctx, commanderID)
	if err != nil {
		return err
	}
	top := energyMax()
	for _, ship := range ships {
		if ship.Energy >= top {
			continue
		}
		if err := queries.UpdateShipBattleState(ctx, db.UpdateShipBattleStateParams{
			Level:       ship.Level,
			Exp:         ship.Exp,
			Intimacy:    ship.Intimacy,
			Energy:      top,
			ID:          ship.ID,
			CommanderID: commanderID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// handleShipLock locks or unlocks ships (UpdateShipLockCommand, CS_12022).
func (game *Game) handleShipLock(client *connection.Client, req *protobuf.CS_12022) error {
	ctx := context.Background()
	err := game.DB.InTx(ctx, func(queries *db.Queries) error {
		for _, id := range req.GetShipIdList() {
			if err := queries.SetShipLocked(ctx, db.SetShipLockedParams{IsLocked: req.GetIsLocked() == 1, ID: id, CommanderID: client.CommanderID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_12023{Result: result(nil)})
}

// handleShipEquip puts equipment from the depot in a ship slot, or empties
// the slot when equip_id is 0 (EquipToShipCommand, UnequipFromShipCommand...,
// CS_12006). What was in the slot goes back to the depot.
func (game *Game) handleShipEquip(client *connection.Client, req *protobuf.CS_12006) error {
	ctx := context.Background()
	err := game.tx(ctx, client, func(tx *Tx) error {
		slots, err := shipEquipments(ctx, tx.Queries, client.CommanderID, req.GetShipId())
		if err != nil {
			return err
		}
		pos := req.GetPos()
		if pos < 1 || pos > shipEquipSlots {
			return errRefused
		}
		slot := slots[pos-1]
		if req.GetEquipId() != 0 {
			if err := tx.SpendEquipment(req.GetEquipId(), 1); err != nil {
				return err
			}
		} else if slot.EquipID == 0 {
			return errRefused
		}
		if slot.EquipID != 0 {
			if err := tx.AddEquipment(slot.EquipID, 1); err != nil {
				return err
			}
		}
		return tx.SetShipEquipment(ctx, db.SetShipEquipmentParams{ShipID: slot.ShipID, Pos: pos, EquipID: req.GetEquipId(), SkinID: slot.SkinID})
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_12007{Result: result(err)})
}

// collectionState is SC_17001, CollectionProxy: the archives (one
// SHIP_STATISTICS_INFO per ship group owned, transform_list the groups
// whose retrofit skin is unlocked), the milestone rewards claimed and the
// medals' progress. The login's syncTechPoints raised the
// entries from the ships owned first (raiseShipGroups).
func collectionState(ctx context.Context, queries *db.Queries, commanderID uint32) (*protobuf.SC_17001, error) {
	groups, err := queries.ListShipGroups(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	trophies, err := loadTrophyState(ctx, queries, commanderID)
	if err != nil {
		return nil, err
	}
	hearts, err := queries.ListShipHeartCounts(ctx)
	if err != nil {
		return nil, err
	}
	heartCounts := make(map[uint32]uint32, len(hearts))
	for _, row := range hearts {
		heartCounts[row.GroupID] = uint32(row.Hearts)
	}
	daily, err := dailyDiscussions(ctx, queries, commanderID)
	if err != nil {
		return nil, err
	}
	awards, err := collectionAwards(ctx, queries, commanderID)
	if err != nil {
		return nil, err
	}
	msg := &protobuf.SC_17001{DailyDiscuss: proto.Uint32(daily), ShipAwardList: awards, ProgressList: trophies.unlocked()}
	for _, group := range groups {
		if !archiveGroups[group.GroupID] {
			continue
		}
		if group.Trans {
			msg.TransformList = append(msg.TransformList, group.GroupID)
		}
		// heart_count is the likes of every commander of the server
		msg.ShipInfoList = append(msg.ShipInfoList, &protobuf.SHIP_STATISTICS_INFO{
			Id:          proto.Uint32(group.GroupID),
			Star:        proto.Uint32(archiveStar(group)),
			HeartFlag:   proto.Uint32(group.HeartFlag),
			HeartCount:  proto.Uint32(heartCounts[group.GroupID]),
			MarryFlag:   proto.Uint32(group.MarryFlag),
			IntimacyMax: proto.Uint32(group.IntimacyMax),
			LvMax:       proto.Uint32(group.LvMax),
		})
	}
	return msg, nil
}

// recordShipGroup adds a new ship to the archives, and returns the SC_17002
// of the archive count medal when its group is new. The group is its
// template's group_type (shipGroup), the one CollectionProxy:flushCollection
// unlocks on the client: 900004's Suffolk is 20304, not 90000. A ship of a
// group already there may still raise its best values (raiseShipGroup).
func recordShipGroup(ctx context.Context, queries *db.Queries, ship db.Ship) (*protobuf.SC_17002, error) {
	best, ok := shipGroupBest(ship)
	if !ok {
		return nil, nil
	}
	added, err := queries.AddShipGroup(ctx, db.AddShipGroupParams{
		CommanderID: best.CommanderID,
		GroupID:     best.GroupID,
		IntimacyMax: best.IntimacyMax,
		LvMax:       best.LvMax,
		MarryFlag:   best.MarryFlag,
		Star:        best.Star,
	})
	if err != nil || added == 0 {
		return nil, err
	}
	groups, err := queries.CountShipGroups(ctx, ship.CommanderID)
	if err != nil {
		return nil, err
	}
	if err := queries.SetTrophy(ctx, db.SetTrophyParams{CommanderID: ship.CommanderID, TrophyID: archiveCountTrophy, Progress: uint32(groups)}); err != nil {
		return nil, err
	}
	return trophyProgress(ctx, queries, ship.CommanderID, archiveCountTrophy)
}

// shipGroupBest is what a ship counts for in its archive entry, as
// CollectionProxy.flushCollection raises the client's ShipGroup with it: its
// template's star (Ship:getStar), level, intimacy and oath (marry_flag 1
// when proposed). False for a ship outside the archives.
func shipGroupBest(ship db.Ship) (db.RaiseShipGroupParams, bool) {
	group := shipGroup(ship.TemplateID)
	if !archiveGroups[group] {
		return db.RaiseShipGroupParams{}, false
	}
	best := db.RaiseShipGroupParams{
		CommanderID: ship.CommanderID,
		GroupID:     group,
		Star:        ownableShips[ship.TemplateID].Star,
		IntimacyMax: ship.Intimacy,
		LvMax:       ship.Level,
	}
	if ship.Propose != 0 {
		best.MarryFlag = 1
	}
	return best, true
}

// raiseShipGroup raises the archive entry of the ship's group to the ship's
// values (shipGroupBest), as the client does whenever BayProxy adds a ship
// or updates one past them (BayProxy:updateShip): the entry keeps them once
// the ship is retired, and the Fleet Technology points counted from it
// (techPoints) with them. A handler that changes a ship's template (limit
// break, retrofit), level, intimacy or oath calls it after saving the ship.
// It tells whether a value was raised.
func raiseShipGroup(ctx context.Context, queries *db.Queries, ship db.Ship) (bool, error) {
	best, ok := shipGroupBest(ship)
	if !ok {
		return false, nil
	}
	raised, err := queries.RaiseShipGroup(ctx, best)
	return raised > 0, err
}

// raiseShipGroups raises the commander's archive entries from every ship
// owned, for the values no raiseShipGroup recorded yet: the stars of the
// ships owned before the archive kept them (migration 0047) and the level
// and affinity battles give.
func raiseShipGroups(ctx context.Context, queries *db.Queries, commanderID uint32) error {
	ships, err := queries.ListShipsByCommander(ctx, commanderID)
	if err != nil {
		return err
	}
	for _, ship := range ships {
		if _, err := raiseShipGroup(ctx, queries, ship); err != nil {
			return err
		}
	}
	return nil
}

// setShipTrophies recomputes the faction medals from the commander's ships
// and stores those that changed.
func setShipTrophies(ctx context.Context, queries *db.Queries, commanderID uint32) error {
	ships, err := queries.ListShipsByCommander(ctx, commanderID)
	if err != nil {
		return err
	}
	trophies, err := queries.ListTrophies(ctx, commanderID)
	if err != nil {
		return err
	}
	stored := map[uint32]uint32{}
	for _, trophy := range trophies {
		stored[trophy.TrophyID] = trophy.Progress
	}
	values := map[uint32]uint32{}
	for _, nation := range nationTrophies {
		values[nation.level120], values[nation.intimacy], values[nation.exp] = 0, 0, 0
	}
	for _, ship := range ships {
		for _, nation := range nationTrophies {
			if shipTemplates[ship.TemplateID].Nationality != nation.nationality {
				continue
			}
			if ship.Level >= nation.level {
				values[nation.level120]++
			}
			values[nation.intimacy] += ship.Intimacy
			values[nation.exp] += ship.Exp
		}
	}
	for _, nation := range nationTrophies {
		for _, id := range []uint32{nation.intimacy, nation.exp, nation.level120} {
			if values[id] == stored[id] {
				continue
			}
			if err := queries.SetTrophy(ctx, db.SetTrophyParams{CommanderID: commanderID, TrophyID: id, Progress: values[id]}); err != nil {
				return err
			}
		}
	}
	return nil
}

// handleShipCount answers GetShipCntCommand (MainCheckShipNumSequence, on
// entering the main scene): how many ships the server keeps, as in
// SC_11003.ship_count. More than the dock the client loaded makes it show
// dockyard_data_loss_detected and log out.
func (game *Game) handleShipCount(client *connection.Client, req *protobuf.CS_11800) error {
	count, err := game.DB.CountShipsByCommander(context.Background(), client.CommanderID)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_11801{ShipCount: proto.Uint32(uint32(count))})
}
