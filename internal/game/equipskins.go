package game

import (
	"context"
	"slices"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// DROP_TYPE_EQUIPMENT_SKIN (const.lua): an equip_skin_template id, added to
// the gear skin box (Drop's case, EquipmentProxy:addEquipmentSkin).
const dropTypeEquipSkin = 9

// equipSkinConfig is an equip_skin_template row: the equipment types it
// dresses (Ship:checkCanEquipSkin matches them against a slot's equip_N).
type equipSkinConfig struct {
	EquipType []uint32 `json:"equip_type"`
}

var equipSkins map[uint32]equipSkinConfig

func loadEquipSkinData() error {
	return gamedata.Load("equip_skin_template", &equipSkins)()
}

// equipSkinBox is SC_14101, EquipmentProxy's equipmentSkinIds: the gear
// skins not worn, by equip_skin_template id.
func equipSkinBox(ctx context.Context, queries *db.Queries, commanderID uint32) (*protobuf.SC_14101, error) {
	rows, err := queries.ListEquipSkins(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	msg := &protobuf.SC_14101{EquipSkinList: make([]*protobuf.EQUIPSKININFO, len(rows))}
	for i, row := range rows {
		msg.EquipSkinList[i] = &protobuf.EQUIPSKININFO{Id: proto.Uint32(row.SkinID), Count: proto.Uint32(row.Count)}
	}
	return msg, nil
}

// AddEquipSkin puts count of a gear skin in the box.
func (tx *Tx) AddEquipSkin(skinID, count uint32) error {
	return tx.Queries.AddEquipSkin(tx.ctx, db.AddEquipSkinParams{CommanderID: tx.commanderID, SkinID: skinID, Count: count})
}

// SpendEquipSkin takes count of a gear skin from the box, ErrNotEnough if
// there are fewer.
func (tx *Tx) SpendEquipSkin(skinID, count uint32) error {
	n, err := tx.Queries.SpendEquipSkin(tx.ctx, db.SpendEquipSkinParams{CommanderID: tx.commanderID, SkinID: skinID, Count: count})
	if err == nil && n == 0 {
		err = ErrNotEnough
	}
	return err
}

// unequipSkins puts the gear skins a ship's slots wear back in the box, as
// the client does for the ships it removes (DestroyShipsCommand,
// ModShipCommand, UpgradeStarCommand: addEquipmentSkin of each
// getEquipSkin); the slots go with the ship.
func (tx *Tx) unequipSkins(equips []db.ShipEquipment) error {
	for _, equip := range equips {
		if equip.SkinID != 0 {
			if err := tx.AddEquipSkin(equip.SkinID, 1); err != nil {
				return err
			}
		}
	}
	return nil
}

// handleShipEquipSkin puts a gear skin from the box on a ship's slot, or
// takes the slot's off when equip_skin_id is 0
// (UpdateShipEquipmentSkinCommand, EquipESkinFormShipCommand, CS_12036). On
// result 0 the client returns the slot's previous skin to the box, takes
// the new one from it and stores it on the slot (Ship:updateEquipmentSkin),
// whatever the slot holds: a skin belongs to the slot, not the equipment,
// which only shows it when its type is one of the skin's. The server does
// the same, refusing a skin the commander has none of, one none of the
// slot's equip_N types fit (Ship:checkCanEquipSkin) and taking off an empty
// slot's.
func (game *Game) handleShipEquipSkin(client *connection.Client, req *protobuf.CS_12036) error {
	ctx := context.Background()
	err := game.tx(ctx, client, func(tx *Tx) error {
		ship, err := tx.GetShip(ctx, db.GetShipParams{ID: req.GetShipId(), CommanderID: client.CommanderID})
		if err != nil {
			return notFound(err)
		}
		slots, err := shipEquipments(ctx, tx.Queries, client.CommanderID, ship.ID)
		if err != nil {
			return err
		}
		pos, skinID := req.GetPos(), req.GetEquipSkinId()
		if pos < 1 || pos > shipEquipSlots {
			return errRefused
		}
		slot := slots[pos-1]
		if skinID != 0 {
			skin, ok := equipSkins[skinID]
			template, known := ownableShips[ship.TemplateID]
			if !ok || !known || !slices.ContainsFunc(template.slotTypes(pos), func(kind uint32) bool {
				return slices.Contains(skin.EquipType, kind)
			}) {
				return errRefused
			}
			if err := tx.SpendEquipSkin(skinID, 1); err != nil {
				return err
			}
		} else if slot.SkinID == 0 {
			return errRefused
		}
		if slot.SkinID != 0 {
			if err := tx.AddEquipSkin(slot.SkinID, 1); err != nil {
				return err
			}
		}
		if err := tx.SetShipEquipment(ctx, db.SetShipEquipmentParams{ShipID: ship.ID, Pos: pos, EquipID: slot.EquipID, SkinID: skinID}); err != nil {
			return err
		}
		// the commander manual counts a gear skin worn
		if skinID != 0 {
			tx.event(TaskEvent{SubType: TaskSubGearSkin, Count: 1})
		}
		return nil
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_12037{Result: result(err)})
}
