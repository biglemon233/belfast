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

// handleRetireShips retires ships (DestroyShipsCommand, CS_12004), from the
// dock's retire mode. On result 0 the client removes each ship, puts its gear
// back in the depot and its gear skins in their box, adds ShipCalcHelper.CalcDestoryRes (the gold of the
// ships' types, their rarity's destory_item) and reads nothing else of the
// reply; the server does the same. Any ship it can't retire refuses the whole
// list, nothing changed.
func (game *Game) handleRetireShips(client *connection.Client, req *protobuf.CS_12004) error {
	ctx := context.Background()
	ids := req.GetShipIdList()
	err := game.tx(ctx, client, func(tx *Tx) error {
		return tx.retireShips(ids)
	})
	if err != nil && !refused(err) {
		return err
	}
	logger.WithFields("Retire",
		logger.FieldValue("ids", fmt.Sprint(ids)),
		logger.FieldValue("refused", refused(err)),
	).Debug("retire")
	reply := &protobuf.SC_12005{Result: result(err)}
	if err == nil {
		reply.ShipIdList = ids
	}
	return client.Send(reply)
}

// retireShips removes the commander's ships of ids, their gear going back to
// the depot, and gives what ShipCalcHelper.CalcDestoryRes says they're worth:
// per ship (Ship:calReturnRes) its ship_data_by_type gold and its
// ship_data_by_star destory_item, merged, the Specialized Core capped by
// gameset urpt_chapter_max. It counts for TaskSubRetireShip.
//
// ShipStatus.canDestroyShip refuses locked, blueprint and META ships and
// those with an onDestroy flag (fleets, sorties, the flagship...), which the
// client asks to clear first: the server refuses the ones in a fleet, the
// active chapter or a secretary slot, the states belfast keeps.
func (tx *Tx) retireShips(ids []uint32) error {
	ctx, queries := tx.ctx, tx.Queries
	if len(ids) == 0 || len(slices.Compact(slices.Sorted(slices.Values(ids)))) != len(ids) {
		logger.WithFields("Retire", logger.FieldValue("ids", fmt.Sprint(ids))).Warn("retire: empty or duplicate ids")
		return errRefused
	}
	ships, err := shipsByID(ctx, queries, tx.commanderID, ids)
	if err != nil {
		return err
	}
	if len(ships) != len(ids) {
		logger.WithFields("Retire", logger.FieldValue("ids", fmt.Sprint(ids)), logger.FieldValue("found", len(ships))).Warn("retire: ships not found")
		return errRefused
	}
	busy, err := busyShips(ctx, queries, tx.commanderID)
	if err != nil {
		return err
	}
	gold := uint32(0)
	var drops []*protobuf.DROPINFO
	for _, id := range ids {
		ship := ships[id]
		// its current template's type and rarity (Ship:calReturnRes reads
		// the ship's own config), limit broken and retrofitted ones too
		template, ok := ownableShips[ship.TemplateID]
		if !ok {
			logger.WithFields("Retire", logger.FieldValue("ship", id), logger.FieldValue("template", ship.TemplateID)).Warn("retire: unknown template")
			return errRefused
		}
		if ship.IsLocked {
			logger.WithFields("Retire", logger.FieldValue("ship", id), logger.FieldValue("template", ship.TemplateID)).Warn("retire: locked")
			return errRefused
		}
		if template.unretirable {
			logger.WithFields("Retire", logger.FieldValue("ship", id), logger.FieldValue("template", ship.TemplateID)).Warn("retire: unretirable template")
			return errRefused
		}
		if busy[id] {
			logger.WithFields("Retire", logger.FieldValue("ship", id), logger.FieldValue("template", ship.TemplateID)).Warn("retire: busy (fleet, secretary, sortie or commission)")
			return errRefused
		}
		equips, err := queries.ListShipEquipments(ctx, id)
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
		// the client puts them back in the gear skin box
		if err := tx.unequipSkins(equips); err != nil {
			return err
		}
		// the archive keeps its best values (and so its tech points) after it
		if _, err := raiseShipGroup(ctx, queries, ship); err != nil {
			return err
		}
		if err := queries.DeleteShip(ctx, db.DeleteShipParams{ID: id, CommanderID: tx.commanderID}); err != nil {
			return err
		}
		gold += retireGold[template.Type]
		for _, item := range configDrops(retireItems[template.Rarity]) {
			drops = mergeDrop(drops, item.GetType(), item.GetId(), item.GetNumber())
		}
	}
	// belfast keeps no Specialized Core limit counter (SC_15001 limit_list
	// says 0), so all of urpt_chapter_max is left
	drops = slices.DeleteFunc(drops, func(drop *protobuf.DROPINFO) bool {
		if drop.GetType() != dropTypeVItem || drop.GetId() != vitemSpecializedCore {
			return false
		}
		drop.Number = proto.Uint32(min(drop.GetNumber(), specializedCoreCap[1]))
		return drop.GetNumber() == 0
	})
	if err := tx.AddResource(ResourceGold, gold); err != nil {
		return err
	}
	if err := tx.grantDrops(drops); err != nil {
		return err
	}
	tx.event(TaskEvent{SubType: TaskSubRetireShip, Count: uint32(len(ids))})
	return nil
}

// busyShips are the commander's ships ShipStatus.canDestroyShip turns down
// for a state belfast keeps: in a fleet (inFleet, inPvP...), in the active
// chapter (inChapter), a secretary (inAdmiral) or on a commission (inEvent).
func busyShips(ctx context.Context, queries *db.Queries, commanderID uint32) (map[uint32]bool, error) {
	busy := map[uint32]bool{}
	fleets, err := queries.ListFleetShips(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	for _, ship := range fleets {
		busy[ship.ShipID] = true
	}
	secretaries, err := queries.ListSecretaries(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	for _, secretary := range secretaries {
		busy[secretary.ShipID] = true
	}
	commissions, err := queries.ListCommissionShips(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	for _, ship := range commissions {
		busy[ship.ShipID] = true
	}
	chapter, err := loadChapter(ctx, queries, commanderID)
	if err != nil || chapter == nil {
		return busy, err
	}
	info := chapter.info
	for _, group := range slices.Concat(info.GetMainGroupList(), info.GetSubmarineGroupList(), info.GetSupportGroupList()) {
		for _, ship := range group.GetShipList() {
			busy[ship.GetId()] = true
		}
	}
	return busy, nil
}
