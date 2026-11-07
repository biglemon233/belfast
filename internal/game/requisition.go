package game

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// Requisition (Build -> Requisition, SupportShipPoolPage) turns Medals of
// Honor into random ships right away. Everything about it is client data:
// gameset supports_config description is [medals per ship,
// [[rarity, weight], ...], ships per month]: 6 medals, Common 5400, Rare
// 3200, Elite 1000, Super Rare 400 (the 54/32/10/4% of gametip
// honor_medal_support_tips_display rate_tip), 999 a month (the rate_tip
// again, and support_times_limited: "You cannot requisition any more this
// month."). The pool is the ship_data_group groups whose description links
// to the Requisition page, ["GETBOAT", {page: 3}] (BuildShipScene.PAGE_SUPPORT,
// view/ship/buildship/buildshipscene.lua), labelled "Requisition (random
// chance)". The client never sees the monthly count: it only shows the tip
// on result 30.
const (
	// BuildShipScene.PAGE_SUPPORT, the GETBOAT page of the Requisition pool
	requisitionPage = 3
	// SC_16101 result the client shows support_times_limited for
	resultRequisitionLimit = 30
)

type requisitionRate struct {
	rarity, weight uint32
	ships          []uint32 // base templates of the rarity in the pool
}

var (
	// medals per ship and ships per month (supports_config)
	requisitionCost, requisitionMonthly uint32
	// the rarities of supports_config that have ships in the pool
	requisitionRates []requisitionRate
	// gameset unoccupied_ship_nationality: ships of these nationalities don't
	// take dock space (BayProxy:getShipCount)
	unoccupiedNationalities []uint32
)

// loadRequisitions reads supports_config and the Requisition pool out of the
// ship groups, once shipTemplates is built (loadShipData).
func loadRequisitions(groups map[uint32]shipGroupConfig) error {
	var config []json.RawMessage
	if err := gamedata.GamesetDescription("supports_config", &config); err != nil {
		return err
	}
	var weights [][2]uint32
	if len(config) < 3 || json.Unmarshal(config[0], &requisitionCost) != nil ||
		json.Unmarshal(config[1], &weights) != nil || json.Unmarshal(config[2], &requisitionMonthly) != nil {
		return gamedata.Missing("gameset", "supports_config [cost, weights, monthly]")
	}
	if err := gamedata.GamesetDescription("unoccupied_ship_nationality", &unoccupiedNationalities); err != nil {
		return err
	}
	byRarity := map[uint32][]uint32{}
	for _, group := range groups {
		if !obtainedFromPage(group.Description, requisitionPage) {
			continue
		}
		if ship, ok := shipTemplates[group.GroupType*10+1]; ok {
			byRarity[ship.Rarity] = append(byRarity[ship.Rarity], group.GroupType*10+1)
		}
	}
	requisitionRates = nil
	for _, w := range weights {
		if ships := byRarity[w[0]]; len(ships) > 0 && w[1] > 0 {
			slices.Sort(ships)
			requisitionRates = append(requisitionRates, requisitionRate{rarity: w[0], weight: w[1], ships: ships})
		}
	}
	return nil
}

// obtainedFromPage is whether a ship_data_group description has a
// ["GETBOAT", {page: page}] link without a projectName (the construction
// pools' links have one).
func obtainedFromPage(description json.RawMessage, page uint32) bool {
	var entries []json.RawMessage
	if json.Unmarshal(description, &entries) != nil {
		return false
	}
	for _, raw := range entries {
		var entry, link []json.RawMessage
		if json.Unmarshal(raw, &entry) != nil || len(entry) < 2 || json.Unmarshal(entry[1], &link) != nil || len(link) < 2 {
			continue
		}
		var action string
		var params struct {
			Page        uint32 `json:"page"`
			ProjectName string `json:"projectName"`
		}
		if json.Unmarshal(link[0], &action) == nil && action == "GETBOAT" &&
			json.Unmarshal(link[1], &params) == nil && params.Page == page && params.ProjectName == "" {
			return true
		}
	}
	return false
}

// pickRequisitionShip draws a rarity by its supports_config weight, then one
// of the pool's ships of that rarity.
func pickRequisitionShip() (uint32, bool) {
	i := weightedPick(rand.Uint32N, requisitionRates, func(r requisitionRate) uint32 { return r.weight })
	if i < 0 {
		return 0, false
	}
	ships := requisitionRates[i].ships
	return ships[rand.IntN(len(ships))], true
}

// handleRequisition answers SupportShipCommand
// (controller/command/ship/supportshipcommand.lua, CS_16100 {cnt}; its
// GetExchangeShipsCommand sibling sends a {time} the proto no longer has and
// nothing registers it). The client checks the dock (getMaxShipBag against
// getShipCount + cnt) and the medals (cost * cnt), lets cnt go from 1 to
// MAX_BUILD_WORK_COUNT (supportshippoolpage.lua), and on result 0 takes the
// medals off and adds ship_list to the dock itself, so no SC_12042 is sent.
// No official capture has one: the reply is built from the Lua alone.
func (game *Game) handleRequisition(client *connection.Client, req *protobuf.CS_16100) error {
	ctx := context.Background()
	count := req.GetCnt()
	var ships []*protobuf.SHIPINFO
	err := errRefused
	if count >= 1 && count <= maxBuilds {
		err = game.tx(ctx, client, func(tx *Tx) (err error) {
			ships, err = tx.requisition(count, time.Now())
			return err
		})
	}
	if err != nil && !refused(err) {
		return err
	}
	if err != nil {
		ships = nil
	}
	return client.Send(&protobuf.SC_16101{Result: result(err), ShipList: ships})
}

// requisition requisitions count ships: within the month's limit and the
// dock, paid for with Medals of Honor.
func (tx *Tx) requisition(count uint32, now time.Time) ([]*protobuf.SHIPINFO, error) {
	month := nextMonth(now).Unix()
	done, err := tx.GetRequisitionCount(tx.ctx, db.GetRequisitionCountParams{CommanderID: tx.commanderID, Month: month})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if done+count > requisitionMonthly {
		return nil, refusal{"requisition limit", resultRequisitionLimit}
	}
	if err := tx.dockHasRoom(count); err != nil {
		return nil, err
	}
	if err := tx.SpendItem(itemHonorMedal, requisitionCost*count); err != nil {
		return nil, err
	}
	ships := make([]*protobuf.SHIPINFO, 0, count)
	for range count {
		template, ok := pickRequisitionShip()
		if !ok {
			return nil, errRefused
		}
		ship, err := tx.AddShip(template)
		if err != nil {
			return nil, err
		}
		ships = append(ships, ship)
	}
	return ships, tx.AddRequisitions(tx.ctx, db.AddRequisitionsParams{CommanderID: tx.commanderID, Month: month, Count: count})
}

// dockHasRoom is whether count more ships fit in the dock, counted as the
// client does (BayProxy:getShipCount leaves out unoccupied_ship_nationality
// ships), with the slots guild technologies add (Player:getMaxShipBag).
func (tx *Tx) dockHasRoom(count uint32) error {
	commander, err := tx.GetCommander(tx.ctx, tx.commanderID)
	if err != nil {
		return err
	}
	owned, err := tx.ListShipsByCommander(tx.ctx, tx.commanderID)
	if err != nil {
		return err
	}
	occupied := uint32(0)
	for _, ship := range owned {
		if !slices.Contains(unoccupiedNationalities, shipTemplates[ship.TemplateID/10*10+1].Nationality) {
			occupied++
		}
	}
	bonus, err := tx.guildTechBonus(guildTechShipBag)
	if err != nil {
		return err
	}
	if occupied+count > commander.ShipBagMax+bonus {
		return errRefused
	}
	return nil
}
