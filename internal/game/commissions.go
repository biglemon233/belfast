package game

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// collection_template row
type commissionConfig struct {
	Type uint32 `json:"type"`
	Lv   uint32 `json:"lv"`   // commander level it is offered from
	Time uint32 `json:"time"` // how long it stays offered, 0 for no limit

	CollectTime uint32   `json:"collect_time"` // seconds it runs
	Oil         uint32   `json:"oil"`          // oil it costs to start
	Exp         uint32   `json:"exp"`          // exp each ship gets
	ShipNum     uint32   `json:"ship_num"`     // ships it takes at least
	ShipLv      uint32   `json:"ship_lv"`      // level one of them must reach
	ShipType    []uint32 `json:"ship_type"`    // the ship types it takes
	// what the panel shows it gives: [{type, id, nums}], nums a count, a
	// range ("100~170") or a minimum ("3000+")
	DropDisplay []commissionDisplay `json:"drop_display"`
	// {type, id, nums: "Chance to receive"}, or [] when it has none
	SpecialDrop json.RawMessage `json:"special_drop"`
}

type commissionDisplay struct {
	Type uint32 `json:"type"`
	ID   uint32 `json:"id"`
	Nums string `json:"nums"`
}

// collection_template types that aren't drawn
const (
	// given by the ACTIVITY_TYPE_COLLECTION_EVENT activity's config_data
	// (model/vo/collectioneventactivity.lua)
	commissionTypeActivity = 0
	// EventConst.EVENT_TYPE_NIGHT (view/event/eventconst.lua), offered at
	// night only (EventProxy:checkNightEvent)
	commissionTypeNight = 5
)

var commissionConfigs map[uint32]commissionConfig

func loadCommissionData() error {
	var resources map[uint32]commissionResourceConfig
	var gameset map[string]uint32
	err := gamedata.LoadAll(
		gamedata.Load("collection_template", &commissionConfigs),
		gamedata.Load("player_resource", &resources),
		func() (err error) {
			gameset, err = gamedata.Gameset()
			return err
		},
	)
	if err != nil {
		return err
	}
	for _, key := range []string{"night_collection_begin", "night_collection_end"} {
		if _, ok := gameset[key]; !ok {
			return gamedata.Missing("gameset", "%s", key)
		}
	}
	nightCommissionBegin, nightCommissionEnd = gameset["night_collection_begin"], gameset["night_collection_end"]
	resourceVirtualItems = resourceItems(resources)
	return readCommissionAwards(resources)
}

// resourceItems is the player_resource itemid of each of the core resources
// (59003 for merit...), for the drops that give the item rather than the
// resource. The field resources share one placeholder (59099), which tells
// nothing of the field it is for, so they are left out.
func resourceItems(resources map[uint32]commissionResourceConfig) map[uint32]uint32 {
	items := map[uint32]uint32{}
	for id, row := range resources {
		if id >= 100 || row.ItemID == 0 || row.ItemID == 59099 {
			continue
		}
		if current, ok := items[row.ItemID]; !ok || id < current {
			items[row.ItemID] = id
		}
	}
	return items
}

// grantCommissionsStarter offers a new commander new_commander.json's count
// of commissions, drawn at random from the collection_template rows open at
// their level that are neither activity nor night ones nor the urgent ones
// with a time limit (pushed after battles, SC_13011). The capture drew 30103,
// 30108, 30107, 30105, 20110 out of these 26 at level 1.
func grantCommissionsStarter(ctx context.Context, queries *db.Queries, commander db.Commander) error {
	var candidates []uint32
	for id, config := range commissionConfigs {
		if config.Lv <= commander.Level && config.Time == 0 && config.Type != commissionTypeActivity && config.Type != commissionTypeNight {
			candidates = append(candidates, id)
		}
	}
	rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	for _, id := range candidates[:min(newCommander.Commissions, len(candidates))] {
		if err := queries.AddCommission(ctx, db.AddCommissionParams{CommanderID: commander.ID, CollectionID: id}); err != nil {
			return err
		}
	}
	return nil
}

// commissionList is SC_13002, EventProxy's commissions (refreshed first,
// refreshCommissions); max_team, how many can run at once, is user_level
// max_collection at the commander's level.
func commissionList(ctx context.Context, queries *db.Queries, commanderID, level uint32) (*protobuf.SC_13002, error) {
	if _, err := refreshCommissions(ctx, queries, commanderID, level, time.Now()); err != nil {
		return nil, err
	}
	list, err := commissionInfos(ctx, queries, commanderID)
	if err != nil {
		return nil, err
	}
	return &protobuf.SC_13002{MaxTeam: proto.Uint32(commanderLevels[level].MaxCollection), CollectionList: list}, nil
}
