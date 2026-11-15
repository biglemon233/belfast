package game

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// The Private Quarters (ApartmentProxy, protocol module 28): the rooms
// opened, the shipgirls living in them with their Intimacy (favor), the
// gifts and furniture owned. Built from the Lua and the dorm3d tables
// alone: the official capture only has an empty SC_28000 (a commander under
// level 80). The wiki's Private Quarters page gives the energy rule: three
// a day ("Each day gives you three energy points"), so it is spent per
// server day (the client only refills its own count on Mondays,
// ApartmentProxy.timeCall, until the next login).

// DROP_TYPE_DORM3D_* (const.lua): what the client adds to ApartmentProxy
// (Drop's DROP_TYPE_DORM3D_* cases, model/vo/drop.lua)
const (
	dropTypeDorm3dFurniture = 26 // dorm3d_furniture_template, to its room
	dropTypeDorm3dGift      = 27 // dorm3d_gift
	dropTypeDorm3dSkin      = 28 // dorm3d_resource, to its shipgirl
)

// TaskSubPrivateQuarters is task 7311's sub type (gameset
// weekly_fixed_task, "Interact with shipgirls in the Private Quarters for 3
// days"): the days the commander visited a room. GUESS: no table says what
// counts as a day's interaction; a visit (CS_28036) is the first one of any.
const TaskSubPrivateQuarters = 500

// TaskSubApartmentEnter is the sub type of the "enter the Private Quarters"
// tasks (20611, 23846, 23920): their awards are the Beach, Café and Workshop
// construction tickets (15022-15024) that open the real rooms the shop's
// invitations are for.
const TaskSubApartmentEnter = 502

// apartmentStarterTasks are the 502 tasks: handed out at login so the first
// entry into the room selection completes them.
var apartmentStarterTasks = []uint32{20611, 23846, 23920}

// apartmentState is a commander's Private Quarters, loaded for a request.
type apartmentState struct {
	commanderID uint32
	vigorUsed   uint32 // energy spent today
	day         uint32 // today's start, unix time
	ships       map[uint32]*db.ApartmentShip
	rooms       map[uint32]*db.ApartmentRoom
	furniture   map[uint32][]db.ApartmentFurniture // by room
	gifts       map[uint32]db.ApartmentGift
	visitDay    uint32
}

// loadApartment reads the commander's Private Quarters as of now: the
// energy and the Intimacy gained today start over each server day.
func loadApartment(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (*apartmentState, error) {
	state := &apartmentState{
		commanderID: commanderID,
		day:         uint32(startOfDay(now).Unix()),
		ships:       map[uint32]*db.ApartmentShip{},
		rooms:       map[uint32]*db.ApartmentRoom{},
		furniture:   map[uint32][]db.ApartmentFurniture{},
		gifts:       map[uint32]db.ApartmentGift{},
	}
	row, err := queries.GetApartmentState(ctx, commanderID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		if row.VigorDay == state.day {
			state.vigorUsed = row.VigorUsed
		}
		state.visitDay = row.VisitDay
	}
	ships, err := queries.ListApartmentShips(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	for _, ship := range ships {
		if ship.FavorDay != state.day {
			ship.DailyFavor, ship.FavorDay = 0, state.day
		}
		state.ships[ship.ShipGroup] = &ship
	}
	rooms, err := queries.ListApartmentRooms(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	for _, room := range rooms {
		state.rooms[room.RoomID] = &room
	}
	furniture, err := queries.ListApartmentFurniture(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	for _, piece := range furniture {
		state.furniture[piece.RoomID] = append(state.furniture[piece.RoomID], piece)
	}
	gifts, err := queries.ListApartmentGifts(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	for _, gift := range gifts {
		state.gifts[gift.GiftID] = gift
	}
	return state, nil
}

// saveShip stores a shipgirl's state.
func (state *apartmentState) saveShip(tx *Tx, ship *db.ApartmentShip) error {
	return tx.SetApartmentShip(tx.ctx, db.SetApartmentShipParams{
		FavorLv: ship.FavorLv, FavorExp: ship.FavorExp, DailyFavor: ship.DailyFavor, FavorDay: ship.FavorDay,
		Triggers: ship.Triggers, Dialogues: ship.Dialogues, Skins: ship.Skins, CurSkin: ship.CurSkin,
		Name: ship.Name, NameCd: ship.NameCd, VisitTime: ship.VisitTime,
		CommanderID: state.commanderID, ShipGroup: ship.ShipGroup,
	})
}

// saveRoom stores a room's collection and invitations.
func (state *apartmentState) saveRoom(tx *Tx, room *db.ApartmentRoom) error {
	return tx.SetApartmentRoom(tx.ctx, db.SetApartmentRoomParams{Collections: room.Collections, Ships: room.Ships, CommanderID: state.commanderID, RoomID: room.RoomID})
}

// apartmentSkins is Apartment.skinList: her default outfit and the ones obtained.
func apartmentSkins(ship *db.ApartmentShip) []uint32 {
	return append([]uint32{dorm3dDorms[ship.ShipGroup].SkinModel}, ship.Skins...)
}

// unlocked is ApartmentProxy.CheckUnlockConfig: whether what a dialogue,
// a collection item or an outfit needs ({type, args...}, none for
// nothing) is met.
func (state *apartmentState) unlocked(ctx context.Context, queries *db.Queries, cond []uint32) (bool, error) {
	if len(cond) == 0 {
		return true, nil
	}
	arg := func(i int) uint32 {
		if i < len(cond) {
			return cond[i]
		}
		return 0
	}
	switch cond[0] {
	case 1: // Intimacy level of a shipgirl
		ship := state.ships[arg(1)]
		return ship != nil && ship.FavorLv >= arg(2), nil
	case 2: // a furniture owned
		furniture := dorm3dFurnitures[arg(1)]
		return slices.ContainsFunc(state.furniture[furniture.RoomID], func(piece db.ApartmentFurniture) bool { return piece.FurnitureID == arg(1) }), nil
	case 3: // a gift given
		return state.gifts[arg(1)].UsedNumber > 0, nil
	case 4: // the shipgirl married
		married, err := queries.GetShipGroupMarried(ctx, db.GetShipGroupMarriedParams{CommanderID: state.commanderID, GroupID: arg(1)})
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return married > 0, err
	case 5: // a shipgirl invited to a common area
		room := state.rooms[arg(1)]
		return room != nil && slices.Contains(room.Ships, arg(2)), nil
	case 6: // an outfit obtained
		ship := state.ships[arg(2)]
		return ship != nil && slices.Contains(apartmentSkins(ship), arg(1)), nil
	}
	return false, nil
}

// spendVigor saves the energy spent today.
func (state *apartmentState) saveVigor(tx *Tx) error {
	return tx.SetApartmentVigor(tx.ctx, db.SetApartmentVigorParams{CommanderID: state.commanderID, VigorUsed: state.vigorUsed, VigorDay: state.day})
}

// triggerFavor is ApartmentProxy.triggerFavor on the server: count times
// trigger, each worth num Intimacy points for is_daily_max energy, nothing
// gained (nor spent) without the energy or once she can't gain more; a
// trigger that can't repeat is remembered. The energy and the shipgirl are
// saved.
func (state *apartmentState) triggerFavor(tx *Tx, ship *db.ApartmentShip, triggerID, count uint32) error {
	trigger := dorm3dFavorTriggers[triggerID]
	room := favorToMax(ship.ShipGroup, ship.FavorLv)
	if trigger.IsDailyMax+state.vigorUsed <= dorm3dVigorMax && ship.FavorLv < dorm3dFavorMaxLevel && ship.FavorExp < room {
		state.vigorUsed += trigger.IsDailyMax * count
		gain := min(trigger.Num*count, room-ship.FavorExp)
		ship.FavorExp += gain
		ship.DailyFavor += gain
	}
	if trigger.IsRepeat == 0 && !slices.Contains(ship.Triggers, triggerID) {
		ship.Triggers = append(ship.Triggers, triggerID)
	}
	if err := state.saveVigor(tx); err != nil {
		return err
	}
	return state.saveShip(tx, ship)
}

// apartmentShipInfo is a shipgirl's APARTMENT_SHIP.
func apartmentShipInfo(ship *db.ApartmentShip, hidden []db.ApartmentHiddenPart) *protobuf.APARTMENT_SHIP {
	info := &protobuf.APARTMENT_SHIP{
		ShipGroup: proto.Uint32(ship.ShipGroup), FavorLv: proto.Uint32(ship.FavorLv), FavorExp: proto.Uint32(ship.FavorExp),
		RegularTrigger: ship.Triggers, DailyFavor: proto.Uint32(ship.DailyFavor), Dialogues: ship.Dialogues,
		Skins: ship.Skins, CurSkin: proto.Uint32(ship.CurSkin), Name: proto.String(ship.Name),
		NameCd: proto.Uint32(ship.NameCd), VisitTime: proto.Uint32(ship.VisitTime),
	}
	for _, part := range hidden {
		if part.ShipGroup == ship.ShipGroup {
			info.HiddenInfo = append(info.HiddenInfo, &protobuf.SKIN_HIDDEN_INFO{SkinId: proto.Uint32(part.SkinID), HiddenParts: part.Parts})
		}
	}
	return info
}

// roomInfo is a room's APARTMENT_ROOM.
func (state *apartmentState) roomInfo(room *db.ApartmentRoom) *protobuf.APARTMENT_ROOM {
	info := &protobuf.APARTMENT_ROOM{Id: proto.Uint32(room.RoomID), Collections: room.Collections, Ships: room.Ships}
	for _, piece := range state.furniture[room.RoomID] {
		info.Furnitures = append(info.Furnitures, &protobuf.APARTMENT_FURNITURE{FurnitureId: proto.Uint32(piece.FurnitureID), SlotId: proto.Uint32(piece.SlotID)})
	}
	return info
}

// personalGroups are the shipgirls whose room is open, by room.
func (state *apartmentState) personalGroups() []uint32 {
	var groups []uint32
	for id := range state.rooms {
		if group := dorm3dRooms[id].personalGroup(); group != 0 {
			groups = append(groups, group)
		}
	}
	slices.Sort(groups)
	return groups
}

// apartmentLogin is SC_28000, the Private Quarters at login: the gifts,
// the shipgirls, the shop counts of the dorm3d goods, the energy spent
// today (daily_vigor_max: ApartmentProxy takes it off dorm3d_set's), the
// rooms, and the Juustagram / chats of every shipgirl whose room is open
// (Dorm3dInsProxy needs a list for her before any SC_28025 about her).
func apartmentLogin(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (*protobuf.SC_28000, error) {
	state, err := loadApartment(ctx, queries, commanderID, now)
	if err != nil {
		return nil, err
	}
	msg := &protobuf.SC_28000{DailyVigorMax: proto.Uint32(state.vigorUsed)}
	for _, id := range sortedKeys(state.gifts) {
		gift := state.gifts[id]
		msg.Gifts = append(msg.Gifts, &protobuf.APARTMENT_GIFT{GiftId: proto.Uint32(gift.GiftID), Number: proto.Uint32(gift.Number), UsedNumber: proto.Uint32(gift.UsedNumber)})
	}
	hidden, err := queries.ListApartmentHiddenParts(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	for _, group := range sortedKeys(state.ships) {
		msg.Ships = append(msg.Ships, apartmentShipInfo(state.ships[group], hidden))
	}
	for _, id := range sortedKeys(state.rooms) {
		msg.Rooms = append(msg.Rooms, state.roomInfo(state.rooms[id]))
	}
	if err := dorm3dShopCounts(ctx, queries, commanderID, now, msg); err != nil {
		return nil, err
	}
	msg.Ins, err = apartmentIns(ctx, queries, commanderID, state.personalGroups())
	return msg, err
}

// dorm3dShopCounts fills SC_28000's shop counts (ApartmentProxy shopCount,
// as ShoppingCommand counts a dorm3d furniture or gift purchase): the goods
// limited by a "count" are counted for good, the ones in a purchase group
// for the group's period.
func dorm3dShopCounts(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time, msg *protobuf.SC_28000) error {
	purchases, err := shopPurchases(ctx, queries, commanderID, now)
	if err != nil {
		return err
	}
	groups, err := shopGroupPurchases(ctx, queries, commanderID, now)
	if err != nil {
		return err
	}
	type counts struct{ daily, permanent map[uint32]uint32 }
	byType := map[uint32]*counts{
		dropTypeDorm3dGift:      {map[uint32]uint32{}, map[uint32]uint32{}},
		dropTypeDorm3dFurniture: {map[uint32]uint32{}, map[uint32]uint32{}},
	}
	for id, goods := range shopTemplate {
		count := byType[goods.Type]
		if count == nil || goods.target == 0 {
			continue
		}
		if goods.Group > 0 && goods.GroupLimit > 0 && groups[goods.Group] > 0 {
			count.daily[goods.target] = groups[goods.Group]
		}
		if goods.hasCount && purchases[id] > 0 {
			count.permanent[goods.target] += purchases[id]
		}
	}
	list := func(counts map[uint32]uint32) []*protobuf.APARTMENT_GIFT_SHOP {
		var list []*protobuf.APARTMENT_GIFT_SHOP
		for _, id := range sortedKeys(counts) {
			list = append(list, &protobuf.APARTMENT_GIFT_SHOP{GiftId: proto.Uint32(id), Count: proto.Uint32(counts[id])})
		}
		return list
	}
	msg.GiftDaily = list(byType[dropTypeDorm3dGift].daily)
	msg.GiftPermanent = list(byType[dropTypeDorm3dGift].permanent)
	msg.FurnitureDaily = list(byType[dropTypeDorm3dFurniture].daily)
	msg.FurniturePermanent = list(byType[dropTypeDorm3dFurniture].permanent)
	return nil
}

// addDorm3dDrop stores a DROP_TYPE_DORM3D_* reward: a furniture for its
// room (once: the client places a piece by id), gifts, an outfit for its
// shipgirl.
func (tx *Tx) addDorm3dDrop(kind, id, count uint32) error {
	switch kind {
	case dropTypeDorm3dFurniture:
		furniture, ok := dorm3dFurnitures[id]
		if !ok {
			return errRefused
		}
		return tx.AddApartmentFurniture(tx.ctx, db.AddApartmentFurnitureParams{CommanderID: tx.commanderID, RoomID: furniture.RoomID, FurnitureID: id})
	case dropTypeDorm3dGift:
		if _, ok := dorm3dGifts[id]; !ok {
			return errRefused
		}
		return tx.AddApartmentGift(tx.ctx, db.AddApartmentGiftParams{CommanderID: tx.commanderID, GiftID: id, Number: count})
	}
	skin, ok := dorm3dSkins[id]
	if !ok {
		return errRefused
	}
	if err := tx.AddApartmentShip(tx.ctx, db.AddApartmentShipParams{CommanderID: tx.commanderID, ShipGroup: skin.ShipGroup}); err != nil {
		return err
	}
	ship, err := tx.GetApartmentShip(tx.ctx, db.GetApartmentShipParams{CommanderID: tx.commanderID, ShipGroup: skin.ShipGroup})
	if err != nil || slices.Contains(ship.Skins, id) {
		return err
	}
	ship.Skins = append(ship.Skins, id)
	slices.Sort(ship.Skins)
	state := &apartmentState{commanderID: tx.commanderID}
	return state.saveShip(tx, &ship)
}

// apartmentTx runs fn on the commander's Private Quarters in a transaction.
func (game *Game) apartmentTx(client *connection.Client, fn func(tx *Tx, state *apartmentState, now time.Time) error) error {
	ctx := context.Background()
	now := time.Now()
	return game.tx(ctx, client, func(tx *Tx) error {
		state, err := loadApartment(ctx, tx.Queries, tx.commanderID, now)
		if err != nil {
			return err
		}
		return fn(tx, state, now)
	})
}

// apartmentReply is the result of an apartmentTx: its refusal or nil; any
// other error is returned as is.
func apartmentReply(err error) (*uint32, error) {
	if err != nil && !refused(err) {
		return nil, err
	}
	return result(err), nil
}

// handleApartmentRoomUnlock answers ApartmentRoomUnlockCommand (CS_28001):
// opens a room for its unlock_item, which the client takes off itself on
// result 0. A personal room moves its shipgirl in at Intimacy level 1 (the
// client makes her Apartment itself) with that level's levelup_item; room
// and ins are the room and her Juustagram / chats.
func (game *Game) handleApartmentRoomUnlock(client *connection.Client, req *protobuf.CS_28001) error {
	reply := &protobuf.SC_28002{}
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		config, ok := dorm3dRooms[req.GetRoomId()]
		if !ok || state.rooms[req.GetRoomId()] != nil {
			return errRefused
		}
		for _, item := range config.unlockItems {
			if item[0] != dropTypeItem {
				return errRefused
			}
			if err := tx.SpendItem(item[1], item[2]); err != nil {
				return err
			}
		}
		if err := tx.AddApartmentRoom(tx.ctx, db.AddApartmentRoomParams{CommanderID: state.commanderID, RoomID: req.GetRoomId()}); err != nil {
			return err
		}
		room := &db.ApartmentRoom{CommanderID: state.commanderID, RoomID: req.GetRoomId()}
		reply.Room = state.roomInfo(room)
		group := config.personalGroup()
		if group == 0 {
			return nil
		}
		if err := tx.AddApartmentShip(tx.ctx, db.AddApartmentShipParams{CommanderID: state.commanderID, ShipGroup: group}); err != nil {
			return err
		}
		// her level 1 reward (the wiki's Intimacy table: "Level 01 ...
		// 1x Bunny Plushie", which the room's first guide has her given);
		// the client only sees it at the next login
		if err := tx.grantDrops(configDrops(dorm3dFavorLevels[group][1].drops)); err != nil {
			return err
		}
		var err error
		reply.Ins, err = apartmentIns(tx.ctx, tx.Queries, state.commanderID, []uint32{group})
		return err
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	if *code != 0 {
		reply = &protobuf.SC_28002{}
	}
	reply.Result = code
	return client.Send(reply)
}

// apartmentShip is the shipgirl whose room is open, nil when none.
func (state *apartmentState) ship(group uint32) (*db.ApartmentShip, error) {
	ship := state.ships[group]
	if ship == nil {
		return nil, errRefused
	}
	return ship, nil
}

// handleApartmentTriggerFavor answers ApartmentTriggerFavorCommand
// (CS_28003): an interaction worth Intimacy (a talk, a touch, private
// time...). The client applies the same triggerFavor on result 0. The
// one-time triggers for owning her (type 5) and marrying her (type 6) need
// that to be true.
func (game *Game) handleApartmentTriggerFavor(client *connection.Client, req *protobuf.CS_28003) error {
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		ship, err := state.ship(req.GetShipGroup())
		if err != nil {
			return err
		}
		trigger, ok := dorm3dFavorTriggers[req.GetTriggerId()]
		if !ok || (trigger.IsRepeat == 0 && slices.Contains(ship.Triggers, req.GetTriggerId())) {
			return errRefused
		}
		switch trigger.Type {
		case favorTriggerOwner:
			owned, err := tx.CountOwnedShipGroup(tx.ctx, db.CountOwnedShipGroupParams{CommanderID: state.commanderID, GroupID: ship.ShipGroup})
			if err != nil {
				return err
			}
			if owned == 0 {
				return errRefused
			}
		case favorTriggerPropose:
			if ok, err := state.unlocked(tx.ctx, tx.Queries, []uint32{4, ship.ShipGroup}); err != nil || !ok {
				return errors.Join(err, errRefused)
			}
		}
		return state.triggerFavor(tx, ship, req.GetTriggerId(), 1)
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_28004{Result: code})
}

// handleApartmentLevelUp answers ApartmentLevelUpCommand (CS_28005): the
// next Intimacy level for its points (Apartment.addLevel), and its
// levelup_item as drop_list (the client adds it; levelup_client_item are
// what it unlocks there itself).
func (game *Game) handleApartmentLevelUp(client *connection.Client, req *protobuf.CS_28005) error {
	var drops []*protobuf.DROPINFO
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		ship, err := state.ship(req.GetShipGroup())
		if err != nil {
			return err
		}
		next, ok := dorm3dFavorLevels[ship.ShipGroup][ship.FavorLv+1]
		if !ok || ship.FavorLv >= dorm3dFavorMaxLevel || ship.FavorExp < next.FavorExp {
			return errRefused
		}
		ship.FavorExp -= next.FavorExp
		ship.FavorLv++
		if err := state.saveShip(tx, ship); err != nil {
			return err
		}
		drops = configDrops(next.drops)
		if err := tx.grantDrops(drops); err != nil {
			return err
		}
		return tx.apartmentEvent(ship.ShipGroup, apartmentEventFavorLevel, ship.FavorLv, now)
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	if *code != 0 {
		drops = nil
	}
	return client.Send(&protobuf.SC_28006{Result: code, DropList: drops})
}

// handleApartmentReplaceFurniture answers ApartmentReplaceFurnitureCommand
// (CS_28007): the furniture placed in a room's slots, applied as the
// client's ApartmentRoom.ReplaceFurniture: a slot's piece is put back,
// then the piece named (0 none) is set in it, from the room's owned pieces
// not placed yet, in a slot it fits.
func (game *Game) handleApartmentReplaceFurniture(client *connection.Client, req *protobuf.CS_28007) error {
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		roomID := req.GetRoomId()
		if state.rooms[roomID] == nil {
			return errRefused
		}
		pieces := slices.Clone(state.furniture[roomID])
		for _, change := range req.GetFurnitures() {
			slot, id := change.GetSlotId(), change.GetFurnitureId()
			if slot == 0 || dorm3dFurnitureSlots[slot].RoomID != roomID {
				return errRefused
			}
			for i := range pieces {
				if pieces[i].SlotID == slot {
					pieces[i].SlotID = 0
				}
			}
			if id == 0 {
				continue
			}
			i := slices.IndexFunc(pieces, func(piece db.ApartmentFurniture) bool { return piece.FurnitureID == id && piece.SlotID == 0 })
			if i < 0 || !slices.Contains(dorm3dFurnitures[id].slots, slot) {
				return errRefused
			}
			pieces[i].SlotID = slot
		}
		for i, piece := range pieces {
			if piece.SlotID == state.furniture[roomID][i].SlotID {
				continue
			}
			if err := tx.SetApartmentFurnitureSlot(tx.ctx, db.SetApartmentFurnitureSlotParams{SlotID: piece.SlotID, CommanderID: state.commanderID, RoomID: roomID, FurnitureID: piece.FurnitureID}); err != nil {
				return err
			}
		}
		return nil
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_28008{Result: code})
}

// handleApartmentGiveGift answers ApartmentGiveGiftCommand (CS_28009):
// gifts given to a shipgirl, one she takes (dorm3d_gift ship_group_id 0 or
// hers), each its favor trigger once (the client: triggerFavor with the
// number). They count as gifts given for her Juustagram unlocks.
func (game *Game) handleApartmentGiveGift(client *connection.Client, req *protobuf.CS_28009) error {
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		ship, err := state.ship(req.GetShipGroup())
		if err != nil {
			return err
		}
		if len(req.GetGifts()) == 0 {
			return errRefused
		}
		for _, given := range req.GetGifts() {
			gift, ok := dorm3dGifts[given.GetGiftId()]
			if !ok || given.GetNumber() == 0 || (gift.ShipGroupID != 0 && gift.ShipGroupID != ship.ShipGroup) {
				return errRefused
			}
			n, err := tx.GiveApartmentGift(tx.ctx, db.GiveApartmentGiftParams{Count: given.GetNumber(), CommanderID: state.commanderID, GiftID: given.GetGiftId()})
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrNotEnough
			}
			if err := state.triggerFavor(tx, ship, gift.FavorTriggerID, given.GetNumber()); err != nil {
				return err
			}
			if err := tx.apartmentEvent(ship.ShipGroup, apartmentEventGift, given.GetNumber(), now); err != nil {
				return err
			}
		}
		return nil
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_28010{Result: code})
}

// handleApartmentCollectItem answers ApartmentCollectionItemCommand
// (CS_28011): an item of a room's collection found, once, when it can be
// (its unlock). An item worth Intimacy (award, a favor trigger) is for the
// shipgirl ship_group (0 in a common area for the items worth none). Items
// found in a personal room count for her Juustagram unlocks.
func (game *Game) handleApartmentCollectItem(client *connection.Client, req *protobuf.CS_28011) error {
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		room := state.rooms[req.GetRoomId()]
		item, ok := dorm3dCollections[req.GetCollectionId()]
		if room == nil || !ok || item.RoomID != room.RoomID || slices.Contains(room.Collections, req.GetCollectionId()) {
			return errRefused
		}
		if ok, err := state.unlocked(tx.ctx, tx.Queries, item.unlock); err != nil || !ok {
			return errors.Join(err, errRefused)
		}
		room.Collections = append(room.Collections, req.GetCollectionId())
		if err := state.saveRoom(tx, room); err != nil {
			return err
		}
		if item.Award > 0 {
			ship, err := state.ship(req.GetShipGroup())
			if err != nil {
				return err
			}
			if err := state.triggerFavor(tx, ship, item.Award, 1); err != nil {
				return err
			}
		}
		if group := dorm3dRooms[room.RoomID].personalGroup(); group != 0 {
			return tx.apartmentEvent(group, apartmentEventCollect, 1, now)
		}
		return nil
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_28012{Result: code})
}

// handleApartmentChangeSkin answers ApartmentChangeSkinCommand
// (CS_28013): the outfit a shipgirl wears, one of hers she can wear (its
// unlock: obtained, or invited where it is worn).
func (game *Game) handleApartmentChangeSkin(client *connection.Client, req *protobuf.CS_28013) error {
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		ship, err := state.ship(req.GetShipGroup())
		if err != nil {
			return err
		}
		skin, ok := dorm3dSkins[req.GetSkin()]
		if req.GetSkin() != 0 && (!ok || skin.ShipGroup != ship.ShipGroup) {
			return errRefused
		}
		if ok, err := state.unlocked(tx.ctx, tx.Queries, skin.unlock); err != nil || !ok {
			return errors.Join(err, errRefused)
		}
		ship.CurSkin = req.GetSkin()
		return state.saveShip(tx, ship)
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_28014{Result: code})
}

// handleApartmentTalk answers ApartmentDoTalkCommand (CS_28015): a
// dialogue played for the first time (later ones are not sent), when it
// can be. No dorm3d_dialogue_group row has a reward: drop_list is empty.
func (game *Game) handleApartmentTalk(client *connection.Client, req *protobuf.CS_28015) error {
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		dialogue, ok := dorm3dDialogues[req.GetDialogId()]
		if !ok {
			return errRefused
		}
		ship, err := state.ship(dialogue.CharID)
		if err != nil {
			return err
		}
		if slices.Contains(ship.Dialogues, req.GetDialogId()) {
			return nil
		}
		if ok, err := state.unlocked(tx.ctx, tx.Queries, dialogue.unlock); err != nil || !ok {
			return errors.Join(err, errRefused)
		}
		ship.Dialogues = append(ship.Dialogues, req.GetDialogId())
		return state.saveShip(tx, ship)
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_28016{Result: code})
}

// handleSelectDorm3dEnter answers SelectDorm3dEnterCommand (CS_28017,
// type 0), sent when the room selection opens: nothing to record.
//
//belfast:static
func (game *Game) handleSelectDorm3dEnter(client *connection.Client, req *protobuf.CS_28017) error {
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		tx.event(TaskEvent{SubType: TaskSubApartmentEnter, Count: 1})
		return nil
	})
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_28018{Result: proto.Uint32(0)})
}

// handleApartmentInvite answers ApartmentRoomInviteUnlockCommand
// (CS_28019): a shipgirl of a common area's character_pay invited to it
// for its invite_cost goods' price (shop_template, discount included: the
// Beach's Sirius invitation is free), which the client takes off itself.
func (game *Game) handleApartmentInvite(client *connection.Client, req *protobuf.CS_28019) error {
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		room := state.rooms[req.GetRoomId()]
		config := dorm3dRooms[req.GetRoomId()]
		group := req.GetShipGroup()
		if room == nil || !slices.Contains(config.invitable, group) || slices.Contains(room.Ships, group) {
			return errRefused
		}
		goods, ok := shopTemplate[config.inviteGoods[group]]
		if !ok {
			return errRefused
		}
		price, ok := goods.price(1, 100, now)
		if !ok {
			return errRefused
		}
		if err := tx.SpendShopResource(goods.ResourceType, price); err != nil {
			return err
		}
		room.Ships = append(room.Ships, group)
		return state.saveRoom(tx, room)
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_28020{Result: code})
}

// maxCallNameLength is Dorm3dLevelLayer.NAME_LONG_SIZE.
const maxCallNameLength = 14

// handleApartmentSetCall answers DormSetCallCommand (CS_28021): what a
// shipgirl calls the commander ("" her default_appellation), changed once
// every drom3d_appellation_time hours.
func (game *Game) handleApartmentSetCall(client *connection.Client, req *protobuf.CS_28021) error {
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		ship, err := state.ship(req.GetShipGroup())
		if err != nil {
			return err
		}
		name := req.GetName()
		if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxCallNameLength || strings.ContainsFunc(name, unicode.IsSpace) || int64(ship.NameCd) > now.Unix() {
			return errRefused
		}
		ship.Name = name
		ship.NameCd = uint32(now.Add(time.Duration(dorm3dCallNameHours) * time.Hour).Unix())
		return state.saveShip(tx, ship)
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_28022{Result: code})
}

// handleApartmentVisit answers Dorm3dRecordVisitCommand (CS_28036, sent
// on entering a personal room): the visit's time. Visits and the days she
// was visited count for her Juustagram unlocks, the days the commander
// visited any for task 7311.
func (game *Game) handleApartmentVisit(client *connection.Client, req *protobuf.CS_28036) error {
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		ship, err := state.ship(req.GetShipId())
		if err != nil {
			return err
		}
		newDay := ship.VisitTime == 0 || !sameServerDay(time.Unix(int64(ship.VisitTime), 0), now)
		ship.VisitTime = uint32(now.Unix())
		if err := state.saveShip(tx, ship); err != nil {
			return err
		}
		if err := tx.apartmentEvent(ship.ShipGroup, apartmentEventVisits, 1, now); err != nil {
			return err
		}
		if newDay {
			if err := tx.apartmentEvent(ship.ShipGroup, apartmentEventVisitDays, 1, now); err != nil {
				return err
			}
		}
		if state.visitDay == state.day {
			return nil
		}
		tx.event(TaskEvent{SubType: TaskSubPrivateQuarters, Count: 1})
		return tx.SetApartmentVisitDay(tx.ctx, db.SetApartmentVisitDayParams{CommanderID: state.commanderID, VisitDay: state.day})
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_28037{Result: code})
}

// handleApartmentHideParts answers ApartmentSkinPartHiddenCommand
// (CS_28038): the parts of one of a shipgirl's outfits she wears hidden.
func (game *Game) handleApartmentHideParts(client *connection.Client, req *protobuf.CS_28038) error {
	err := game.apartmentTx(client, func(tx *Tx, state *apartmentState, now time.Time) error {
		ship, err := state.ship(req.GetShipGroup())
		if err != nil {
			return err
		}
		if skin, ok := dorm3dSkins[req.GetSkinId()]; !ok || skin.ShipGroup != ship.ShipGroup {
			return errRefused
		}
		return tx.SetApartmentHiddenParts(tx.ctx, db.SetApartmentHiddenPartsParams{CommanderID: state.commanderID, ShipGroup: ship.ShipGroup, SkinID: req.GetSkinId(), Parts: req.GetHiddenParts()})
	})
	code, err := apartmentReply(err)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_28039{Result: code})
}
