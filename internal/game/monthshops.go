package game

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// CS_16201 types: the shop a purchase is from (ShamShoppingCommand.SHAM_SHOP,
// FragmentShoppingCommand.FRAG_SHOP / FRAG_NORMAL_SHOP,
// QuotaShoppingCommand.QUOTA_SHOP).
const (
	monthShopCore           = 1 // Core Data exchange, ShamBattleShop
	monthShopBlueprint      = 2 // Prototype shop, Goods.TYPE_FRAGMENT
	monthShopBlueprintLimit = 3 // Prototype shop, Goods.TYPE_FRAGMENT_NORMAL
	monthShopQuota          = 4 // QuotaShop
)

// monthShop is a month_shop_template row, id the month (1-12) the stock is
// on sale (MonthlyShop:isOpen).
type monthShop struct {
	Core           []uint32 `json:"core_shop_goods"`
	Blueprint      []uint32 `json:"blueprint_shop_goods"`
	BlueprintLimit []uint32 `json:"blueprint_shop_limit_goods"`
}

// activityGoods is an activity_shop_template row: num of the commodity for
// resource_num of the price, num_limit times (0 no limit).
type activityGoods struct {
	CommodityType    uint32          `json:"commodity_type"`
	CommodityID      uint32          `json:"commodity_id"`
	Num              uint32          `json:"num"`
	NumLimit         uint32          `json:"num_limit"`
	ResourceCategory uint32          `json:"resource_category"` // DROP_TYPE_RESOURCE or DROP_TYPE_ITEM
	ResourceType     uint32          `json:"resource_type"`
	ResourceNum      uint32          `json:"resource_num"`
	LimitArgs        json.RawMessage `json:"limit_args"`
}

var (
	monthShops     map[uint32]monthShop
	activityShop   map[uint32]activityGoods
	quotaShopGoods []uint32
	// the quota shop's goods: how many of the commodity one may own
	// (limit_args {"quota", n}, QuotaCommodity:GetLimitGoodCount)
	quotaLimits map[uint32]uint32
)

func loadMonthShops() error {
	var quotaShops map[uint32]struct {
		Goods []uint32 `json:"shop_goods"`
	}
	err := gamedata.LoadAll(
		gamedata.Load("month_shop_template", &monthShops),
		gamedata.Load("activity_shop_template", &activityShop),
		gamedata.Load("quota_shop_template", &quotaShops),
	)
	if err != nil {
		return err
	}
	// QuotaShop.Ctor reads row 1
	quotaShopGoods = quotaShops[1].Goods
	quotaLimits = map[uint32]uint32{}
	for _, id := range quotaShopGoods {
		var args [][]json.RawMessage
		if json.Unmarshal(activityShop[id].LimitArgs, &args) != nil {
			continue
		}
		for _, arg := range args {
			var name string
			var n uint32
			if len(arg) == 2 && json.Unmarshal(arg[0], &name) == nil && name == "quota" && json.Unmarshal(arg[1], &n) == nil {
				quotaLimits[id] = n
			}
		}
	}
	return nil
}

// monthKey is the month purchases of the monthly shops count in: year * 100
// + month, server time.
func monthKey(now time.Time) uint32 {
	now = now.In(gamedata.Zone())
	return uint32(now.Year()*100 + int(now.Month()))
}

// monthShopInfo is SC_16200, the monthly shops' stock: the month on sale and
// what was bought of it, the Core Data exchange's in core_shop_list, the
// Prototype shop's in blue_shop_list and its limited goods (never reset) in
// normal_shop_list (ShopsProxy, ShamBattleShop:update, FragmentShop:update).
// The official capture's account bought nothing and gets only the month.
func monthShopInfo(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (*protobuf.SC_16200, error) {
	month := uint32(now.In(gamedata.Zone()).Month())
	rows, err := queries.ListMonthShopPurchases(ctx, db.ListMonthShopPurchasesParams{CommanderID: commanderID, Month: monthKey(now)})
	if err != nil {
		return nil, err
	}
	shop := monthShops[month]
	msg := &protobuf.SC_16200{Month: proto.Uint32(month)}
	for _, row := range rows {
		info := &protobuf.SHOPINFO{ShopId: proto.Uint32(row.GoodsID), PayCount: proto.Uint32(row.Count)}
		switch {
		case slices.Contains(shop.Core, row.GoodsID):
			msg.CoreShopList = append(msg.CoreShopList, info)
		case slices.Contains(shop.Blueprint, row.GoodsID):
			msg.BlueShopList = append(msg.BlueShopList, info)
		case slices.Contains(shop.BlueprintLimit, row.GoodsID):
			msg.NormalShopList = append(msg.NormalShopList, info)
		}
	}
	return msg, nil
}

// handleMonthShopping answers ShamShoppingCommand
// (controller/command/sham/shamshoppingcommand.lua), FragmentShoppingCommand
// and QuotaShoppingCommand (controller/command/shop/): count times the
// activity_shop_template id from the shop type says. On result 0 the client
// counts the purchase (not in the quota shop, limited by what one owns),
// takes the price off itself and adds drop_list. The client checks the
// price and the limit; so does the server, along with the goods being on
// sale this month. No official capture has a purchase: the reply is built
// from the Lua alone.
func (game *Game) handleMonthShopping(client *connection.Client, req *protobuf.CS_16201) error {
	var drops []*protobuf.DROPINFO
	err := game.tx(context.Background(), client, func(tx *Tx) (err error) {
		drops, err = tx.buyMonthShopGoods(req.GetType(), req.GetId(), req.GetCount(), time.Now())
		if err == nil && req.GetType() == monthShopCore {
			tx.event(TaskEvent{SubType: TaskSubCoreShop, Count: 1})
		}
		return err
	})
	if err != nil && !refused(err) {
		return err
	}
	if err != nil {
		logger.WithFields("MonthShop", logger.FieldValue("type", req.GetType()), logger.FieldValue("id", req.GetId())).Warn("purchase refused")
		drops = nil
	}
	return client.Send(&protobuf.SC_16202{Result: result(err), DropList: drops})
}

// buyMonthShopGoods buys count of a monthly shop's goods: checks it is on
// sale in the shop with enough purchases left, pays, gives the commodity and
// counts the purchase. It returns the drops given.
func (tx *Tx) buyMonthShopGoods(shopType, id, count uint32, now time.Time) ([]*protobuf.DROPINFO, error) {
	goods, ok := activityShop[id]
	if !ok || count == 0 {
		return nil, errRefused
	}
	shop := monthShops[uint32(now.In(gamedata.Zone()).Month())]
	month := monthKey(now)
	var stock []uint32
	switch shopType {
	case monthShopCore:
		stock = shop.Core
	case monthShopBlueprint:
		stock = shop.Blueprint
	case monthShopBlueprintLimit:
		stock, month = shop.BlueprintLimit, 0
	case monthShopQuota:
		stock = quotaShopGoods
	}
	if !slices.Contains(stock, id) {
		return nil, errRefused
	}
	left, err := tx.monthShopGoodsLeft(shopType, id, goods, month, now)
	if err != nil {
		return nil, err
	}
	if count > left {
		return nil, errRefused
	}
	price := goods.ResourceNum * count
	switch goods.ResourceCategory {
	case dropTypeResource:
		err = tx.SpendShopResource(goods.ResourceType, price)
	case dropTypeItem:
		err = tx.SpendItem(goods.ResourceType, price)
	default:
		err = errRefused
	}
	if err != nil {
		return nil, err
	}
	drops := []*protobuf.DROPINFO{dropInfo(goods.CommodityType, goods.CommodityID, goods.Num*count)}
	if err := tx.grantDrops(drops); err != nil {
		return nil, err
	}
	if shopType == monthShopQuota {
		return drops, nil
	}
	return drops, tx.AddMonthShopPurchase(tx.ctx, db.AddMonthShopPurchaseParams{CommanderID: tx.commanderID, GoodsID: id, Month: month, Count: count})
}

// monthShopGoodsLeft is how many more of a goods can be bought
// (ActivityCommodity:GetPurchasableCnt, QuotaCommodity:GetPurchasableCnt): a
// skin once if not owned, the quota shop's goods up to their quota of the
// commodity owned, the others num_limit times a month (for good, the
// Prototype shop's limited goods), without limit at 0.
func (tx *Tx) monthShopGoodsLeft(shopType, id uint32, goods activityGoods, month uint32, now time.Time) (uint32, error) {
	if goods.CommodityType == dropTypeSkin {
		owned, err := ownedSkins(tx.ctx, tx.Queries, tx.commanderID, now)
		if err != nil || hasSkin(owned, goods.CommodityID) {
			return 0, err
		}
		return 1, nil
	}
	if shopType == monthShopQuota {
		limit, ok := quotaLimits[id]
		if !ok {
			return 0, nil
		}
		owned, err := tx.ownedCount(goods.CommodityType, goods.CommodityID)
		return limit - min(limit, owned), err
	}
	if goods.NumLimit == 0 {
		return ^uint32(0), nil
	}
	rows, err := tx.ListMonthShopPurchases(tx.ctx, db.ListMonthShopPurchasesParams{CommanderID: tx.commanderID, Month: month})
	if err != nil {
		return 0, err
	}
	bought := uint32(0)
	for _, row := range rows {
		if row.GoodsID == id {
			bought += row.Count
		}
	}
	return goods.NumLimit - min(goods.NumLimit, bought), nil
}

// ownedCount is Drop:getOwnedCount (model/vo/drop.lua) for the quota shop's
// commodities: equipment counts its whole group (every enhancement level), in
// the depot and on ships; furniture the pieces owned; meowfficers the cats of
// the template (CommanderProxy:GetSameConfigIdCommanderCount).
func (tx *Tx) ownedCount(kind, id uint32) (uint32, error) {
	total := uint32(0)
	switch kind {
	case dropTypeEquip:
		group := equipGroups[id]
		depot, err := tx.ListEquipments(tx.ctx, tx.commanderID)
		if err != nil {
			return 0, err
		}
		for _, row := range depot {
			if equipGroups[row.EquipID] == group {
				total += row.Count
			}
		}
		equipped, err := tx.ListShipEquipmentsByCommander(tx.ctx, tx.commanderID)
		if err != nil {
			return 0, err
		}
		for _, row := range equipped {
			if row.EquipID != 0 && equipGroups[row.EquipID] == group {
				total++
			}
		}
	case dropTypeFurniture:
		rows, err := tx.ListFurniture(tx.ctx, tx.commanderID)
		if err != nil {
			return 0, err
		}
		for _, row := range rows {
			if row.FurnitureID == id {
				total += row.Count
			}
		}
	case dropTypeMeowfficer:
		cats, err := tx.ListMeowfficers(tx.ctx, tx.commanderID)
		if err != nil {
			return 0, err
		}
		for _, cat := range cats {
			if cat.TemplateID == id {
				total++
			}
		}
	}
	return total, nil
}
