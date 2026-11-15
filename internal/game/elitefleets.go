package game

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// Elite fleets (model/vo/chapter.lua eliteFleetList): the fleets a chapter
// keeps for itself instead of FleetProxy's, shared by the chapters of a
// chapter_template.formation. Hard mode chapters (type chapterCustomFleet)
// are fought with their surface and submarine teams; any chapter with a
// support_group_num takes a support team too.

// chapter_template.type (Chapter.SelectFleet / Chapter.CustomFleet)
const (
	chapterSelectFleet = 1
	chapterCustomFleet = 2
)

// ship_data_by_type.team_type (TeamType.Vanguard / Main / Submarine)
const (
	teamVanguard  = "vanguard"
	teamMain      = "main"
	teamSubmarine = "submarine"
)

// teamShipMax is TeamType.VanguardMax / MainMax / SubmarineMax.
const teamShipMax = 3

// shipBundles is ShipType.BundleList (model/const/shiptype.lua), the ship
// types a chapter_template.limitation name stands for.
var shipBundles = map[string][]uint32{
	"zhan":         {4, 5},      // battlecruiser, battleship
	"hang":         {6, 7},      // light, fleet carrier
	"qian":         {8, 17, 22}, // submarines
	"zhong":        {3, 18},     // heavy, large cruiser
	"fanqian":      {1, 2, 20},  // destroyer, light cruiser, vanguard DDG
	"quzhu":        {1, 21, 20}, // destroyer, DDGs
	"fengfan":      {22, 23, 24},
	"qianpaiquzhu": {1, 20},
}

// shipLimit is one fleet slot of chapter_template.limitation: a ship type,
// a ShipType.BundleList name, or neither (any ship).
type shipLimit struct {
	shipType uint32
	bundle   string
}

func parseShipLimit(value any) (shipLimit, error) {
	switch value := value.(type) {
	case float64:
		return shipLimit{shipType: uint32(value)}, nil
	case string:
		if _, ok := shipBundles[value]; !ok {
			return shipLimit{}, fmt.Errorf("unknown ship bundle %q", value)
		}
		return shipLimit{bundle: value}, nil
	}
	return shipLimit{}, fmt.Errorf("slot %v", value)
}

// allows is ShipType.ContainInLimitBundle.
func (limit shipLimit) allows(shipType uint32) bool {
	if limit.bundle != "" {
		return slices.Contains(shipBundles[limit.bundle], shipType)
	}
	return limit.shipType == 0 || limit.shipType == shipType
}

// order is ChapterProxy.SortRecommendLimitation's key: ship types first
// (highest first), then bundles, then free slots.
func (limit shipLimit) order() int {
	switch {
	case limit.bundle != "":
		return 0
	case limit.shipType == 0:
		return 1
	}
	return -int(limit.shipType)
}

// fitsLimits is the check of Chapter:EliteShipTypeFilter, which drops the
// ships it can't seat: each ship in turn takes the first slot (sorted) that
// allows its type.
func fitsLimits(types []uint32, limits []shipLimit) bool {
	free := slices.Clone(limits)
	slices.SortStableFunc(free, func(a, b shipLimit) int { return a.order() - b.order() })
	for _, shipType := range types {
		i := slices.IndexFunc(free, func(limit shipLimit) bool { return limit.allows(shipType) })
		if i < 0 {
			return false
		}
		free = slices.Delete(free, i, i+1)
	}
	return true
}

func anyLimits() []shipLimit { return make([]shipLimit, teamShipMax) }

func bundleLimits(bundle string) []shipLimit {
	return []shipLimit{{bundle: bundle}, {bundle: bundle}, {bundle: bundle}}
}

// checkEliteFleet validates a chapter's FLEET_INFO the way the client builds
// it (Chapter:EliteShipTypeFilter, EliteCommanderFilter): at most group_num
// surface teams of up to three main and three vanguard ships fitting the
// chapter's limitation, submarine_num submarine teams and support_group_num
// support teams (three carriers, or submarines on submarine_support stages),
// owned ships each used once, the teams' cats as checkTeamMeowfficers (those
// no longer owned taken out, Chapter:EliteCommanderFilter). Teams past
// the counts may be sent padded but empty. A SelectFleet chapter is fought
// with FleetProxy's fleets, its surface and submarine teams are dropped as
// the filter does. It returns the fleet to keep, under the formation's id.
func checkEliteFleet(ctx context.Context, queries *db.Queries, commanderID uint32, template *chapterTemplate, fleet *protobuf.FLEET_INFO) (*protobuf.FLEET_INFO, error) {
	refuse := func(reason string) error {
		logger.WithFields("Chapter",
			logger.FieldValue("id", template.id),
			logger.FieldValue("reason", reason),
		).Warn("elite fleet refused")
		return errRefused
	}
	logger.WithFields("Chapter",
		logger.FieldValue("id", template.id),
		logger.FieldValue("main", fmt.Sprint(teamShips(fleet.GetMainTeam()))),
		logger.FieldValue("sub", fmt.Sprint(teamShips(fleet.GetSubmarineTeam()))),
		logger.FieldValue("support", fmt.Sprint(teamShips(fleet.GetSupportTeam()))),
	).Debug("elite fleet")
	kept := &protobuf.FLEET_INFO{
		Id:            proto.Uint32(template.formation),
		MainTeam:      fleet.GetMainTeam(),
		SubmarineTeam: fleet.GetSubmarineTeam(),
		SupportTeam:   fleet.GetSupportTeam(),
	}
	if template.fleetType != chapterCustomFleet {
		kept.MainTeam, kept.SubmarineTeam = nil, nil
	}
	var ids []uint32
	for _, teams := range [][]*protobuf.TEAM_INFO{kept.MainTeam, kept.SubmarineTeam, kept.SupportTeam} {
		for _, team := range teams {
			ids = append(ids, team.GetShipList()...)
		}
	}
	if len(slices.Compact(slices.Sorted(slices.Values(ids)))) != len(ids) {
		return nil, refuse("duplicate ships")
	}
	var teams []*protobuf.TEAM_INFO
	for _, kind := range [][]*protobuf.TEAM_INFO{kept.MainTeam, kept.SubmarineTeam, kept.SupportTeam} {
		teams = append(teams, kind...)
	}
	if err := checkTeamMeowfficers(ctx, queries, commanderID, teams, true); err != nil {
		return nil, err
	}
	ships, err := shipsByID(ctx, queries, commanderID, ids)
	if err != nil {
		return nil, err
	}
	// the ship types of a team by team type, in the team's order
	teamTypes := func(team *protobuf.TEAM_INFO) (map[string][]uint32, bool) {
		types := map[string][]uint32{}
		for _, id := range team.GetShipList() {
			ship, ok := ships[id]
			if !ok {
				return nil, false
			}
			// a ship_data_template id is its group and star: the type is
			// ship_data_statistics' of the group's first star (a ship the
			// table only has at a later star, a retrofit, is its own row)
			template, ok := shipTemplates[ship.TemplateID/10*10+1]
			if !ok {
				template, ok = shipTemplates[ship.TemplateID]
			}
			if !ok {
				// a newer client's ship the data predates: its type is
				// unknown, so no limit can be checked for it
				logger.WithFields("Chapter", logger.FieldValue("ship", ship.TemplateID)).Warn("unknown ship type")
				continue
			}
			shipType := template.Type
			team := shipTeams[shipType]
			types[team] = append(types[team], shipType)
		}
		return types, true
	}
	for i, team := range kept.MainTeam {
		types, ok := teamTypes(team)
		if !ok || len(types) > 0 && uint32(i) >= template.groupNum || len(types[teamSubmarine]) > 0 {
			return nil, refuse("main team type")
		}
		limits := [2][]shipLimit{anyLimits(), anyLimits()}
		if i < len(template.limitation) {
			limits = template.limitation[i]
		}
		// the client enforces the limitation itself (EliteShipTypeFilter);
		// its tables can name slots differently than shipBundles does, so a
		// mismatch only warns
		if !fitsLimits(types[teamMain], limits[0]) || !fitsLimits(types[teamVanguard], limits[1]) {
			logger.WithFields("Chapter",
				logger.FieldValue("id", template.id),
				logger.FieldValue("team", i),
				logger.FieldValue("main", fmt.Sprint(types[teamMain])),
				logger.FieldValue("vanguard", fmt.Sprint(types[teamVanguard])),
			).Warn("main team outside the limitation")
		}
	}
	supportLimit := bundleLimits("hang")
	if template.submarineSupport {
		supportLimit = bundleLimits("qian")
	}
	for _, kind := range []struct {
		teams  []*protobuf.TEAM_INFO
		max    uint32
		limits []shipLimit
		team   string // the team type every ship must be of, "" any
	}{
		{kept.SubmarineTeam, template.submarineNum, anyLimits(), teamSubmarine},
		{kept.SupportTeam, template.supportNum, supportLimit, ""},
	} {
		for i, team := range kind.teams {
			types, ok := teamTypes(team)
			if !ok || len(team.GetShipList()) > 0 && uint32(i) >= kind.max {
				return nil, refuse("sub/support team count")
			}
			var all []uint32
			for teamType, list := range types {
				if kind.team != "" && teamType != kind.team {
					return nil, refuse("sub/support team type")
				}
				all = append(all, list...)
			}
			if !fitsLimits(all, kind.limits) {
				logger.WithFields("Chapter", logger.FieldValue("id", template.id), logger.FieldValue("team", i)).Warn("sub/support team outside the limitation")
			}
		}
	}
	return kept, nil
}

// teamShips is the teams' ship ids, for the tracking logs.
func teamShips(teams []*protobuf.TEAM_INFO) [][]uint32 {
	ships := make([][]uint32, len(teams))
	for i, team := range teams {
		ships[i] = team.GetShipList()
	}
	return ships
}

func saveEliteFleet(ctx context.Context, queries *db.Queries, commanderID uint32, fleet *protobuf.FLEET_INFO) error {
	data, err := proto.Marshal(fleet)
	if err != nil {
		return err
	}
	return queries.SaveChapterEliteFleet(ctx, db.SaveChapterEliteFleetParams{CommanderID: commanderID, Formation: fleet.GetId(), Fleet: data})
}

// eliteFleets is SC_13001.fleet_list, the saved fleets by formation
// (Chapter.BuildEliteFleetInfo), without the ships no longer owned.
func eliteFleets(ctx context.Context, queries *db.Queries, commanderID uint32, owned []db.Ship) ([]*protobuf.FLEET_INFO, error) {
	rows, err := queries.ListChapterEliteFleets(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	ids := make(map[uint32]bool, len(owned))
	for _, ship := range owned {
		ids[ship.ID] = true
	}
	fleets := make([]*protobuf.FLEET_INFO, 0, len(rows))
	for _, row := range rows {
		fleet := &protobuf.FLEET_INFO{}
		if err := proto.Unmarshal(row.Fleet, fleet); err != nil {
			return nil, err
		}
		for _, teams := range [][]*protobuf.TEAM_INFO{fleet.MainTeam, fleet.SubmarineTeam, fleet.SupportTeam} {
			for _, team := range teams {
				team.ShipList = slices.DeleteFunc(team.ShipList, func(id uint32) bool { return !ids[id] })
			}
		}
		fleets = append(fleets, fleet)
	}
	return fleets, nil
}

// handleUpdateCustomFleet saves a chapter's elite fleets
// (UpdateCustomFleetCommand, CS_13107), sent with the chapter's whole
// eliteFleetList (Chapter.PackEliteFleetInfo, id 0) whenever its fleet
// select panel is closed (LevelFleetView onCancelHard / onCancelSupport),
// normal chapters included. id is the chapter; the fleets are kept for its
// formation. The client already applied the edit locally and only reads
// result: non-zero shows update_custom_fleet's error tip.
func (game *Game) handleUpdateCustomFleet(client *connection.Client, req *protobuf.CS_13107) error {
	ctx := context.Background()
	template := chapterTemplates[req.GetId()]
	if template == nil {
		return client.Send(&protobuf.SC_13108{Result: result(errRefused)})
	}
	fleet, err := checkEliteFleet(ctx, game.DB.Queries, client.CommanderID, template, req.GetFleet())
	if refused(err) {
		return client.Send(&protobuf.SC_13108{Result: result(err)})
	}
	if err != nil {
		return err
	}
	if err := saveEliteFleet(ctx, game.DB.Queries, client.CommanderID, fleet); err != nil {
		return err
	}
	return client.Send(&protobuf.SC_13108{Result: result(nil)})
}

// handleRemoveEliteTargetShip takes a ship out of every elite fleet
// (RemoveEliteTargerShipCommand, CS_13111): sent once the commander agrees
// to move a ship that is in a hard mode or support fleet (ShipStatus
// inElite / inSupport, retrofitting, ChapterProxy:RemoveUnitFromSupportFleet).
// The client replaces all its elite fleets with fleet_list
// (ChapterProxy:setEliteCache) and reads nothing else, so the reply is the
// whole saved list, as SC_13001 has it. Nothing was captured: built from the
// Lua alone.
func (game *Game) handleRemoveEliteTargetShip(client *connection.Client, req *protobuf.CS_13111) error {
	ctx := context.Background()
	reply := &protobuf.SC_13112{}
	err := game.tx(ctx, client, func(tx *Tx) error {
		ships, err := tx.ListShipsByCommander(ctx, tx.commanderID)
		if err != nil {
			return err
		}
		fleets, err := eliteFleets(ctx, tx.Queries, tx.commanderID, ships)
		if err != nil {
			return err
		}
		for _, fleet := range fleets {
			removed := false
			for _, teams := range [][]*protobuf.TEAM_INFO{fleet.MainTeam, fleet.SubmarineTeam, fleet.SupportTeam} {
				for _, team := range teams {
					if i := slices.Index(team.ShipList, req.GetShipId()); i >= 0 {
						team.ShipList = slices.Delete(team.ShipList, i, i+1)
						removed = true
					}
				}
			}
			if removed {
				if err := saveEliteFleet(ctx, tx.Queries, tx.commanderID, fleet); err != nil {
					return err
				}
			}
		}
		reply.FleetList = fleets
		return nil
	})
	if err != nil {
		return err
	}
	return client.Send(reply)
}
