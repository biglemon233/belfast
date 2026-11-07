package game

import (
	"context"
	"slices"
	"time"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// itemHonorMedal is ITEM_ID_SILVER_HOOK (const.lua), the Medal of Honor the
// medal exchange sells for (MedalShop:GetResList).
const itemHonorMedal = 15001

// medalGoodsSelectable is the honormedal_goods_list goods_type whose
// purchases each give one of goods, picked by the player (MedalGoods:Selectable,
// the MedalShopPurchasePanel); the others give goods[1].
const medalGoodsSelectable = 2

// medalShopPurchases is how many of each honormedal_goods_list row the
// commander bought from the stock refreshing at next.
func medalShopPurchases(ctx context.Context, queries *db.Queries, commanderID uint32, next time.Time) (map[uint32]uint32, error) {
	rows, err := queries.ListMedalShopPurchases(ctx, db.ListMedalShopPurchasesParams{CommanderID: commanderID, FlashTime: next.Unix()})
	if err != nil {
		return nil, err
	}
	bought := make(map[uint32]uint32, len(rows))
	for _, row := range rows {
		bought[row.GoodsID] += row.Count
	}
	return bought, nil
}

// handleMedalShopPurchase answers MedalShopCommand
// (controller/command/guild/medalshopcommand.lua, the medal exchange of the
// shops despite its folder): shopid is the honormedal_goods_list id,
// flash_time the item_flash_time of the stock the client shows, selected
// the drop ids picked with how many of each. On result 0 the client takes
// price times the purchases off its medals, lowers the goods' count by as
// many and adds drop_list. The client checks the medals and the count left;
// so does the server, along with the stock the goods are from. No official
// capture has a purchase: the reply is built from the Lua alone.
func (game *Game) handleMedalShopPurchase(client *connection.Client, req *protobuf.CS_16108) error {
	var drops []*protobuf.DROPINFO
	err := game.tx(context.Background(), client, func(tx *Tx) (err error) {
		drops, err = tx.buyMedalGoods(req.GetShopid(), req.GetFlashTime(), req.GetSelected(), time.Now())
		return err
	})
	if err != nil && !refused(err) {
		return err
	}
	if err != nil {
		logger.WithFields("MedalShop", logger.FieldValue("id", req.GetShopid()), logger.FieldValue("flash_time", req.GetFlashTime())).Warn("purchase refused")
		drops = nil
	}
	return client.Send(&protobuf.SC_16109{Result: result(err), DropList: drops})
}

// buyMedalGoods buys the selected drops of a medal exchange goods: checks it
// is in the current stock with enough purchases left, spends the medals,
// gives the drops and counts the purchases. It returns the drops given.
func (tx *Tx) buyMedalGoods(id, flashTime uint32, selected []*protobuf.SELECTED_INFO, now time.Time) ([]*protobuf.DROPINFO, error) {
	next := nextMonth(now)
	goods, ok := medalGoods[id]
	// a stock the shop has since refreshed from is turned down: the client
	// fetches the new one when its timer runs out (MedalShopPage:OnTimeOut)
	if !ok || int64(flashTime) != next.Unix() || !slices.Contains(medalShopGoods(tx.commanderID, next), id) || len(selected) == 0 {
		return nil, errRefused
	}
	choices := goods.Goods
	if goods.GoodsType != medalGoodsSelectable {
		choices = choices[:min(len(choices), 1)]
	}
	total := uint32(0)
	var drops []*protobuf.DROPINFO
	for _, pick := range selected {
		if pick.GetCount() == 0 || pick.GetCount() > goods.PurchaseLimit || !slices.Contains(choices, pick.GetId()) {
			return nil, errRefused
		}
		if total += pick.GetCount(); total > goods.PurchaseLimit {
			return nil, errRefused
		}
		drops = mergeDrop(drops, goods.Type, pick.GetId(), goods.Num*pick.GetCount())
	}
	bought, err := medalShopPurchases(tx.ctx, tx.Queries, tx.commanderID, next)
	if err != nil {
		return nil, err
	}
	// MedalGoods:CanPurchaseCnt
	if total > goods.PurchaseLimit-min(bought[id], goods.PurchaseLimit) {
		return nil, errRefused
	}
	if err := tx.SpendItem(itemHonorMedal, goods.Price*total); err != nil {
		return nil, err
	}
	if err := tx.grantDrops(drops); err != nil {
		return nil, err
	}
	err = tx.AddMedalShopPurchase(tx.ctx, db.AddMedalShopPurchaseParams{CommanderID: tx.commanderID, GoodsID: id, FlashTime: next.Unix(), Count: total})
	return drops, err
}
