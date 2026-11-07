package game

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"math/rand/v2"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// GuildConst.GET_SHOP, AUTO_REFRESH and MANUAL_REFRESH: CS_60033's type,
// the shop fetched when the shops open, again when its timer runs out
// (GuildShopPage:OnTimeOut), refreshed for guild tokens (its refresh button).
const (
	guildShopGet           = 0
	guildShopAutoRefresh   = 1
	guildShopManualRefresh = 2
)

// guildGoodsSelectable is the guild_store goods_type whose purchases each
// give one of goods, picked by the player (GuildGoods:Selectable, the
// GuildShopPurchasePanel); the others give goods[1].
const guildGoodsSelectable = 2

// guild_store row
type guildGoodsConfig struct {
	// what one purchase gives: num of a drop of type, goods[1] or, for
	// goods_type guildGoodsSelectable, one of goods (GuildGoods)
	Type          uint32   `json:"type"`
	Goods         []uint32 `json:"goods"`
	GoodsType     uint32   `json:"goods_type"`
	Num           uint32   `json:"num"`
	Price         uint32   `json:"price"` // in guild tokens, per purchase
	PurchaseLimit uint32   `json:"goods_purchase_limit"`
	// Ensure rows are in every stock, the others drawn by weight
	Ensure uint32 `json:"ensure"`
	Weight uint32 `json:"weight"`
}

var (
	guildGoods map[uint32]guildGoodsConfig
	// guildShopEnsured are the guild_store ids every stock has, guildShopPool
	// the ones drawn to fill it, both by id
	guildShopEnsured, guildShopPool []uint32
	// guildShopSize is guildset store_goods_quantity, the goods in a stock
	guildShopSize uint32
	// guildShopRefreshDays are guildset store_refresh's key_args, the days
	// of the week (1 Monday) the stock refreshes on at midnight
	guildShopRefreshDays []uint32
	// guildShopResets and guildShopResetCost are guildset store_reset_time
	// and store_reset_cost (GuildShop:GetResetConsume): the manual refreshes
	// a stock allows (GuildShop:CanRefresh lets one through while
	// refresh_count is 0) and the guild tokens each takes
	guildShopResets, guildShopResetCost uint32
)

func loadGuildShop() error {
	goods, err := gamedata.Table[guildGoodsConfig]("guild_store")
	if err != nil {
		return err
	}
	guildGoods, guildShopEnsured, guildShopPool = goods, nil, nil
	for _, id := range slices.Sorted(maps.Keys(goods)) {
		switch {
		case goods[id].Ensure != 0:
			guildShopEnsured = append(guildShopEnsured, id)
		case goods[id].Weight > 0:
			guildShopPool = append(guildShopPool, id)
		}
	}
	for key, value := range map[string]*uint32{
		"store_goods_quantity": &guildShopSize,
		"store_reset_time":     &guildShopResets,
		"store_reset_cost":     &guildShopResetCost,
	} {
		if err := guildSetting(key, value); err != nil {
			return err
		}
	}
	refresh, ok := guildSet["store_refresh"]
	if !ok {
		return gamedata.Missing("guildset", "store_refresh")
	}
	guildShopRefreshDays = nil
	if err := json.Unmarshal(refresh.KeyArgs, &guildShopRefreshDays); err != nil {
		return gamedata.Missing("guildset", "store_refresh days")
	}
	if len(guildShopRefreshDays) == 0 || slices.ContainsFunc(guildShopRefreshDays, func(day uint32) bool { return day < 1 || day > 7 }) {
		return gamedata.Missing("guildset", "store_refresh days")
	}
	return nil
}

// nextGuildShopRefresh returns when the guild shop refreshes next: the
// first midnight, server time, after now on one of guildShopRefreshDays.
func nextGuildShopRefresh(now time.Time) time.Time {
	day := nextDay(now)
	for !slices.Contains(guildShopRefreshDays, serverWeekday(day)) {
		day = day.AddDate(0, 0, 1)
	}
	return day
}

// guildShopGoods draws a commander's guild shop stock, the guild_store ids
// at each GOODS_INFO_P60 index (from 1): the ensured goods, then goods drawn
// by weight, each once, up to guildShopSize. The draw is seeded by the
// commander, when the stock refreshes and its manual refreshes, so the stock
// stays the same until it refreshes without being stored.
func guildShopGoods(commanderID uint32, next int64, refreshCount uint32) []uint32 {
	random := rand.New(rand.NewPCG(uint64(commanderID)<<32|uint64(refreshCount), uint64(next)))
	goods := slices.Clone(guildShopEnsured)
	pool := slices.Clone(guildShopPool)
	weight := func(id uint32) uint32 { return guildGoods[id].Weight }
	for uint32(len(goods)) < guildShopSize {
		i := weightedPick(random.Uint32N, pool, weight)
		if i < 0 {
			break
		}
		goods = append(goods, pool[i])
		pool = slices.Delete(pool, i, i+1)
	}
	return goods
}

// guildShop is the commander's guild shop stock at now: a stock past its
// refresh time is replaced by the next one, without manual refreshes or
// purchases.
func (tx *Tx) guildShop(now time.Time) (db.CommanderGuildShop, error) {
	next := nextGuildShopRefresh(now).Unix()
	shop, err := tx.GetGuildShop(tx.ctx, tx.commanderID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return shop, err
	}
	if err == nil && shop.NextRefreshTime == next {
		return shop, nil
	}
	shop = db.CommanderGuildShop{CommanderID: tx.commanderID, NextRefreshTime: next}
	return shop, tx.resetGuildShop(shop)
}

// resetGuildShop stores a new stock, nothing bought from it yet.
func (tx *Tx) resetGuildShop(shop db.CommanderGuildShop) error {
	if err := tx.ClearGuildShopPurchases(tx.ctx, tx.commanderID); err != nil {
		return err
	}
	return tx.SetGuildShop(tx.ctx, db.SetGuildShopParams(shop))
}

// guildShopPurchases is how many of each slot of its current stock the
// commander bought.
func (tx *Tx) guildShopPurchases() (map[uint32]uint32, error) {
	rows, err := tx.ListGuildShopPurchases(tx.ctx, tx.commanderID)
	if err != nil {
		return nil, err
	}
	bought := make(map[uint32]uint32, len(rows))
	for _, row := range rows {
		bought[row.Slot] = row.Count
	}
	return bought, nil
}

// guildShopInfo is a stock as SC_60034 carries it: each goods' count is
// what can still be bought of it (GuildGoods:CanPurchase).
func (tx *Tx) guildShopInfo(shop db.CommanderGuildShop) (*protobuf.SHOP_INFO, error) {
	bought, err := tx.guildShopPurchases()
	if err != nil {
		return nil, err
	}
	info := &protobuf.SHOP_INFO{
		RefreshCount:    proto.Uint32(shop.RefreshCount),
		NextRefreshTime: proto.Uint32(uint32(shop.NextRefreshTime)),
	}
	for i, id := range guildShopGoods(tx.commanderID, shop.NextRefreshTime, shop.RefreshCount) {
		slot, limit := uint32(i+1), guildGoods[id].PurchaseLimit
		info.GoodList = append(info.GoodList, &protobuf.GOODS_INFO_P60{
			Id:    proto.Uint32(id),
			Count: proto.Uint32(limit - min(bought[slot], limit)),
			Index: proto.Uint32(slot),
		})
	}
	return info, nil
}

// handleGuildShop answers GetGuildShopCommand (CS_60033): the commander's
// guild shop, its stock refreshing at midnight on the guildset
// store_refresh days; type GuildConst.MANUAL_REFRESH first buys a new stock
// for store_reset_cost guild tokens, once per stock (store_reset_time). On
// result 0 the client replaces its shop with the reply and, for a manual
// refresh, takes the tokens itself; otherwise it shows
// ERROR_MESSAGE[result]. Outside a guild the shop is empty, as the official
// server answered (captured), but with the next refresh time rather than
// the captured 0, which would run the shop page's refresh timer out at once.
// The stock itself is built from the Lua and guild_store alone.
func (game *Game) handleGuildShop(client *connection.Client, req *protobuf.CS_60033) error {
	ctx, now := context.Background(), time.Now()
	var info *protobuf.SHOP_INFO
	err := game.tx(ctx, client, func(tx *Tx) error {
		_, inGuild, err := guildMembership(ctx, tx.Queries, client.CommanderID)
		if err != nil {
			return err
		}
		if !inGuild {
			if req.GetType() == guildShopManualRefresh {
				return errNotInGuild
			}
			info = &protobuf.SHOP_INFO{NextRefreshTime: proto.Uint32(uint32(nextGuildShopRefresh(now).Unix()))}
			return nil
		}
		shop, err := tx.guildShop(now)
		if err != nil {
			return err
		}
		if req.GetType() == guildShopManualRefresh {
			if shop.RefreshCount >= guildShopResets {
				return errRefused
			}
			if err := tx.SpendResource(ResourceGuildCoin, guildShopResetCost); err != nil {
				return err
			}
			shop.RefreshCount++
			if err := tx.resetGuildShop(shop); err != nil {
				return err
			}
		}
		info, err = tx.guildShopInfo(shop)
		return err
	})
	if err != nil && !refused(err) {
		return err
	}
	if err != nil {
		info = nil
	}
	return client.Send(&protobuf.SC_60034{Result: result(err), Info: info})
}

// handleGuildShopPurchase answers GuildShopCommand (CS_60035): goodsid is
// the guild_store id at index in the stock, selected the drop ids picked
// with how many of each (goods[1] once for goods that aren't selectable).
// On result 0 the client lowers the goods' count by the purchases, takes
// price times as many guild tokens and adds drop_list; otherwise it shows
// ERROR_MESSAGE[result]. The client checks the tokens and the count left;
// so does the server, along with the goods being at index in the current
// stock (one that has refreshed since is turned down, the shop page fetches
// it when its timer runs out). Built from the Lua alone.
func (game *Game) handleGuildShopPurchase(client *connection.Client, req *protobuf.CS_60035) error {
	var drops []*protobuf.DROPINFO
	err := game.tx(context.Background(), client, func(tx *Tx) (err error) {
		drops, err = tx.buyGuildGoods(req.GetGoodsid(), req.GetIndex(), req.GetSelected(), time.Now())
		if err == nil {
			tx.event(TaskEvent{SubType: TaskSubFleetShop, Count: 1})
		}
		return err
	})
	if err != nil && !refused(err) {
		return err
	}
	if err != nil {
		drops = nil
	}
	return client.Send(&protobuf.SC_60036{Result: result(err), DropList: drops})
}

// buyGuildGoods buys the selected drops of the guild shop goods id at slot
// index of the commander's stock: checks it is there with enough purchases
// left, spends the guild tokens, gives the drops and counts the purchases.
// It returns the drops given.
func (tx *Tx) buyGuildGoods(id, index uint32, selected []*protobuf.GUILD_SHOP_INFO, now time.Time) ([]*protobuf.DROPINFO, error) {
	_, inGuild, err := guildMembership(tx.ctx, tx.Queries, tx.commanderID)
	if err != nil {
		return nil, err
	}
	if !inGuild {
		return nil, errNotInGuild
	}
	shop, err := tx.guildShop(now)
	if err != nil {
		return nil, err
	}
	stock := guildShopGoods(tx.commanderID, shop.NextRefreshTime, shop.RefreshCount)
	if index == 0 || int(index) > len(stock) || stock[index-1] != id || len(selected) == 0 {
		return nil, errRefused
	}
	goods := guildGoods[id]
	choices := goods.Goods
	if goods.GoodsType != guildGoodsSelectable {
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
	bought, err := tx.guildShopPurchases()
	if err != nil {
		return nil, err
	}
	// GuildGoods:CanPurchaseCnt
	if total > goods.PurchaseLimit-min(bought[index], goods.PurchaseLimit) {
		return nil, errRefused
	}
	if err := tx.SpendResource(ResourceGuildCoin, goods.Price*total); err != nil {
		return nil, err
	}
	if err := tx.grantDrops(drops); err != nil {
		return nil, err
	}
	err = tx.AddGuildShopPurchase(tx.ctx, db.AddGuildShopPurchaseParams{CommanderID: tx.commanderID, Slot: index, Count: total})
	return drops, err
}
