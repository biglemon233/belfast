package game

import (
	"context"
	"maps"
	"math/rand/v2"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// handleChargeList answers GetChargeListCommand: pay_list is what the
// commander bought of the money goods (the charged list the client builds,
// its pay_count the goods' purchase count), first_pay_list the first-purchase
// doubling goods not bought yet, normal_list what was bought of the gift
// packages (ShopsProxy:GetNormalByID, counted against their limit_args
// "time"), normal_group_list of the shop groups, since they last reset.
func (game *Game) handleChargeList(client *connection.Client, req *protobuf.CS_16104) error {
	ctx, now := context.Background(), time.Now()
	purchases, err := shopPurchases(ctx, game.DB.Queries, client.CommanderID, now)
	if err != nil {
		return err
	}
	groups, err := shopGroupPurchases(ctx, game.DB.Queries, client.CommanderID, now)
	if err != nil {
		return err
	}
	reply := &protobuf.SC_16105{}
	for _, id := range slices.Sorted(maps.Keys(chargeGoods)) {
		count, bought := purchases[id]
		if !bought {
			if chargeGoods[id].FirstPayDouble != 0 {
				reply.FirstPayList = append(reply.FirstPayList, id)
			}
			continue
		}
		reply.PayList = append(reply.PayList, &protobuf.SHOPINFO{ShopId: proto.Uint32(id), PayCount: proto.Uint32(count)})
	}
	for _, id := range slices.Sorted(maps.Keys(purchases)) {
		if _, charge := chargeGoods[id]; charge {
			continue
		}
		if genre := shopTemplate[id].Genre; genre == genreGiftPackage || genre == genreNewServerShop {
			reply.NormalList = append(reply.NormalList, &protobuf.SHOPINFO{ShopId: proto.Uint32(id), PayCount: proto.Uint32(purchases[id])})
		}
	}
	for _, group := range slices.Sorted(maps.Keys(groups)) {
		reply.NormalGroupList = append(reply.NormalGroupList, &protobuf.SHOPINFO{ShopId: proto.Uint32(group), PayCount: proto.Uint32(groups[group])})
	}
	return client.Send(reply)
}

// honormedal_goods_list row
type medalGoodsConfig struct {
	Group         uint32 `json:"group"`
	Order         uint32 `json:"order"`
	PurchaseLimit uint32 `json:"goods_purchase_limit"`
	// what one purchase gives: num of a drop of type, goods[1] or, for
	// goods_type medalGoodsSelectable, one of goods (MedalGoods)
	Type      uint32   `json:"type"`
	Goods     []uint32 `json:"goods"`
	GoodsType uint32   `json:"goods_type"`
	Num       uint32   `json:"num"`
	Price     uint32   `json:"price"` // in medals of honor, per purchase
}

var medalGoods map[uint32]medalGoodsConfig

// medalShopGroups are the honormedal_goods_list groups the medal exchange
// stocks, group descending (the official order), each with its rows by id.
// The table doesn't say which rows are on sale: its order is the slot
// MedalShop sorts the goods by (model/vo/shop/medalshop.lua), and newer rows
// reuse the slot of the ones they replace (each General Blueprint series
// takes slots 6 and 7). The official shop stocks, for every slot, its newest
// group (highest group id), which is exactly the captured shop: the series 8
// blueprints but no older series, and one of the SR ship boxes of groups
// 20003 and 20004.
var medalShopGroups [][]uint32

func loadMedalShop() error {
	goods, err := gamedata.Table[medalGoodsConfig]("honormedal_goods_list")
	if err != nil {
		return err
	}
	newest := map[uint32]uint32{}
	for _, row := range goods {
		newest[row.Order] = max(newest[row.Order], row.Group)
	}
	rows := map[uint32][]uint32{}
	for id, row := range goods {
		if newest[row.Order] == row.Group {
			rows[row.Group] = append(rows[row.Group], id)
		}
	}
	groups := slices.Sorted(maps.Keys(rows))
	medalGoods, medalShopGroups = goods, nil
	for _, group := range slices.Backward(groups) {
		slices.Sort(rows[group])
		medalShopGroups = append(medalShopGroups, rows[group])
	}
	return nil
}

// medalShopGoods draws a commander's medal exchange for the month ending at
// next: one row of each stocked group, uniformly (the table has no weights;
// the captured shop is one draw for groups 20003 and 20004). The draw is
// seeded by both, so the shop stays the same until it refreshes without
// being stored.
func medalShopGoods(commanderID uint32, next time.Time) []uint32 {
	random := rand.New(rand.NewPCG(uint64(commanderID), uint64(next.Unix())))
	goods := make([]uint32, 0, len(medalShopGroups))
	for _, rows := range medalShopGroups {
		goods = append(goods, rows[random.IntN(len(rows))])
	}
	return goods
}

// handleMedalShop answers GetMedalShopCommand: this month's medal exchange,
// count is what can still be bought of each goods (its purchase limit less
// what was bought from this stock, MedalShopCommand); it refreshes with the
// month.
func (game *Game) handleMedalShop(client *connection.Client, req *protobuf.CS_16106) error {
	next := nextMonth(time.Now())
	bought, err := medalShopPurchases(context.Background(), game.DB.Queries, client.CommanderID, next)
	if err != nil {
		return err
	}
	reply := &protobuf.SC_16107{
		Result:        result(nil),
		ItemFlashTime: proto.Uint32(uint32(next.Unix())),
	}
	for _, id := range medalShopGoods(client.CommanderID, next) {
		left := medalGoods[id].PurchaseLimit - min(bought[id], medalGoods[id].PurchaseLimit)
		reply.GoodList = append(reply.GoodList, &protobuf.GOODS_INFO_P16{Id: proto.Uint32(id), Count: proto.Uint32(left)})
	}
	return client.Send(reply)
}

// shopStreetGoods is the general shop's stock (shop_template ids of genre
// shopping_street and their discount): navalacademy_shoppingstreet_template
// level 1 sells 10 goods. The server's draw isn't in the client, so this is
// the captured stock.
var shopStreetGoods = [][2]uint32{
	{31132, 90}, {31022, 100}, {31112, 100}, {31111, 80}, {31142, 100},
	{31122, 100}, {24002, 90}, {31011, 100}, {21029, 100}, {31012, 100},
}

// handleShopStreet answers GetShopStreetCommand, the general shop: level 1,
// every item in stock once (buy_count is what's left, 0 once bought since
// the refresh), no manual refresh. It refreshes at the same hours as the
// exercise attempts (the captured refresh was the 12:00 one).
func (game *Game) handleShopStreet(client *connection.Client, req *protobuf.CS_22101) error {
	now := time.Now()
	purchases, err := shopPurchases(context.Background(), game.DB.Queries, client.CommanderID, now)
	if err != nil {
		return err
	}
	street := &protobuf.SHOPPINGSTREET{
		Lv:            proto.Uint32(1),
		NextFlashTime: proto.Uint32(uint32(nextExerciseReset(now).Unix())),
	}
	for _, goods := range shopStreetGoods {
		left := uint32(0)
		if purchases[goods[0]] == 0 {
			left = 1
		}
		street.GoodsList = append(street.GoodsList, &protobuf.STREETGOODS{
			GoodsId: proto.Uint32(goods[0]), Discount: proto.Uint32(goods[1]), BuyCount: proto.Uint32(left),
		})
	}
	return client.Send(&protobuf.SC_22102{Street: street})
}

// handleSkinPreviewUsage acknowledges the skin shop's live2d preview toggle
// (RecordSkinAnimPreviwBtnUsageCommand,
// controller/command/shop/recordskinanimpreviwbtnusagecommand.lua, CS_16203):
// flag is 1 when the preview was switched on. The client keeps the toggle in
// PlayerPrefs and only shows an error tip when ret isn't 0.
// Telemetry only: ignored for now; could feed usage stats later.
//
//belfast:static
func (game *Game) handleSkinPreviewUsage(client *connection.Client, req *protobuf.CS_16203) error {
	return client.Send(&protobuf.SC_16204{Ret: proto.Uint32(0)})
}
