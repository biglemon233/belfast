package game

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// ActivityConst.ACTIVITY_TYPE_PT_SHOP: an event's exchange shop (活动商店),
// its goods the activity_shop_template ids in config_data. The page shows
// each goods' price and how many are left (num_limit bought so far); a buy
// is an ActivityOperationCommand (CS_11202) naming the goods and how many.
const activityTypePtShop = 14

// ActivityConst.ACTIVITY_TYPE_META_SHOP: the META shop (商店 → META, activity
// 7104 here), its goods the activity_shop_template ids in config_data, each
// bought with 破碎的结晶 (itemMetaBrokenCrystal). A buy is an
// ActivityOperationCommand too, but through MetaShoppingCommand: arg1 the
// goods and arg2 the picks' total, the picks themselves in kvargs1 (key the
// commodity, value how many of it), since one goods may let the commander
// choose (commodity_id_list, the TA gear materials) and one purchase is num
// of the pick. The ships it sells are DROP_TYPE_SHIP commodities, whose
// limit_args keep them to one while not owned ("uniqueship") and gate the
// exclusive ones on the ship (LIMIT_ARGS_META_SHIP_EXISTENCE) and their sale
// date (LIMIT_ARGS_SALE_START_TIME).
const activityTypeMetaShop = 119

// ActivityConst.ACTIVITY_TYPE_EVENT_PT: an event's point page (51607 here),
// activity_event_pt its point item and steps; the shop of the event (type 14)
// reads its currency from it.
const activityTypeEventPt = 174

// activityTypeEventPtExchange is ACTIVITY_TYPE_PT_EXCHANGE (51603 here): an
// event's pt exchange, config_id its pt item.
const activityTypeEventPtExchange = 16

// virtualItemBalance is the commander's balance of a virtual item, 0 when it
// has none.
func virtualItemBalance(ctx context.Context, q *db.Queries, commanderID, itemID uint32) (uint32, error) {
	if itemID == 0 {
		return 0, nil
	}
	amount, err := q.GetVirtualItem(ctx, db.GetVirtualItemParams{CommanderID: commanderID, ItemID: itemID})
	if err != nil && errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return amount, err
}

// eventPt is an activity_event_pt row.
type eventPt struct {
	Pt     uint32   `json:"pt"`
	Target []uint32 `json:"target"`
	// the kill page's (type 74) phases: each one's reward at the same index
	// as its target threshold, and the days (1-based) they open on
	DropClient [][]uint32 `json:"drop_client"`
	DayUnlock  []uint32   `json:"day_unlock"`
}

var eventPts map[uint32]eventPt

func loadEventPtData() error {
	return gamedata.Load("activity_event_pt", &eventPts)()
}

// eventPtBalance is the commander's balance of an event's point item, 0 when
// it has none.
func eventPtBalance(ctx context.Context, q *db.Queries, commanderID, activityID uint32) (uint32, error) {
	config, ok := eventPts[activityID]
	if !ok || config.Pt == 0 {
		return 0, nil
	}
	amount, err := q.GetVirtualItem(ctx, db.GetVirtualItemParams{CommanderID: commanderID, ItemID: config.Pt})
	if err != nil && errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return amount, err
}

// shopRecords are the shop's bought counts as the page reads them
// (ActivityShop.Ctor): data1_list the goods bought and data2_list how many of
// each, the two lists read at the same index.
func shopRecords(ctx context.Context, q *db.Queries, commanderID, activityID uint32) (ids, counts []uint32, err error) {
	rows, err := q.ListNewServerShopGoods(ctx, db.ListNewServerShopGoodsParams{CommanderID: commanderID, ActivityID: activityID})
	if err != nil {
		return nil, nil, err
	}
	slices.SortFunc(rows, func(a, b db.ListNewServerShopGoodsRow) int { return cmp.Compare(a.GoodsID, b.GoodsID) })
	for _, row := range rows {
		if row.Bought == 0 {
			continue
		}
		ids = append(ids, row.GoodsID)
		counts = append(counts, row.Bought)
	}
	return ids, counts, nil
}

// shopRecordList is the same bought counts as a key-value list, key the goods
// and value how many were bought: the other shape a page may read them in.
func shopRecordList(ctx context.Context, q *db.Queries, commanderID, activityID uint32) ([]*protobuf.KEYVALUELIST_P11, error) {
	rows, err := q.ListNewServerShopGoods(ctx, db.ListNewServerShopGoodsParams{CommanderID: commanderID, ActivityID: activityID})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b db.ListNewServerShopGoodsRow) int { return cmp.Compare(a.GoodsID, b.GoodsID) })
	var list []*protobuf.KEYVALUELIST_P11
	for _, row := range rows {
		if row.Bought == 0 {
			continue
		}
		list = append(list, &protobuf.KEYVALUELIST_P11{Key: proto.Uint32(row.GoodsID), Value: proto.Uint32(row.Bought)})
	}
	return list, nil
}

// shopCurrency is the balance of an activity shop's currency: the virtual
// item its goods cost (resource_category 8), 0 for the shops paying with a
// player resource or a bag item.
func shopCurrency(ctx context.Context, q *db.Queries, commanderID uint32, config activityConfig) (uint32, error) {
	var ids []uint32
	if json.Unmarshal(config.ConfigData, &ids) != nil || len(ids) == 0 {
		return 0, nil
	}
	goods, ok := activityShop[ids[0]]
	if !ok || goods.ResourceCategory != dropTypeVItem {
		return 0, nil
	}
	amount, err := q.GetVirtualItem(ctx, db.GetVirtualItemParams{CommanderID: commanderID, ItemID: goods.ResourceType})
	if err != nil && errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return amount, err
}

// activityShopOperation buys goods of an activity shop (CS_11202): arg1 (or
// arg_list[0]) is the goods, arg2 (or arg_list[1]) how many, one by default.
// The goods costs resource_num of its resource (a player resource, a bag item
// or a virtual item: the event's pt) per unit, gives num of its commodity
// each, and is limited to num_limit purchases. The purchases are kept like a
// new commander shop's (new_server_shop_goods), keyed by the activity.
func (game *Game) activityShopOperation(client *connection.Client, config activityConfig, req *protobuf.CS_11202) error {
	var goodsIDs []uint32
	if json.Unmarshal(config.ConfigData, &goodsIDs) != nil || len(goodsIDs) == 0 {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	goodsID, count := req.GetArg1(), req.GetArg2()
	if goodsID == 0 && len(req.GetArgList()) > 0 {
		goodsID = req.GetArgList()[0]
	}
	if count == 0 && len(req.GetArgList()) > 1 {
		count = req.GetArgList()[1]
	}
	if count == 0 {
		count = 1
	}
	goods, ok := activityShop[goodsID]
	if !ok || !slices.Contains(goodsIDs, goodsID) || count == 0 || goods.Num == 0 {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	logger.WithFields("ActivityShop",
		logger.FieldValue("activity", config.ID),
		logger.FieldValue("cmd", req.GetCmd()),
		logger.FieldValue("goods", goodsID),
		logger.FieldValue("count", count),
	).Info("buy")
	reply := &protobuf.SC_11203{Result: result(nil)}
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		rows, err := tx.ListNewServerShopGoods(tx.ctx, db.ListNewServerShopGoodsParams{CommanderID: tx.commanderID, ActivityID: config.ID})
		if err != nil {
			return err
		}
		bought := uint32(0)
		for _, row := range rows {
			if row.GoodsID == goodsID {
				bought = row.Bought
			}
		}
		if goods.NumLimit > 0 && bought+count > goods.NumLimit {
			return errRefused
		}
		if err := tx.spendActivityGoods(goods, count); err != nil {
			return err
		}
		drop := dropInfo(goods.CommodityType, goods.CommodityID, goods.Num*count)
		reply.AwardList = []*protobuf.DROPINFO{drop}
		if err := tx.grantDrops(reply.AwardList); err != nil {
			return err
		}
		return tx.AddNewServerShopGoods(tx.ctx, db.AddNewServerShopGoodsParams{
			CommanderID: tx.commanderID, ActivityID: config.ID, GoodsID: goodsID, Bought: count,
		})
	})
	if refused(err) {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}

// spendActivityGoods pays for count of one purchase of an activity shop's
// goods: resource_num of its resource_category each.
func (tx *Tx) spendActivityGoods(goods activityGoods, count uint32) error {
	price := goods.ResourceNum * count
	switch goods.ResourceCategory {
	case dropTypeResource:
		return tx.SpendShopResource(goods.ResourceType, price)
	case dropTypeItem:
		return tx.SpendItem(goods.ResourceType, price)
	case dropTypeVItem:
		n, err := tx.SpendVirtualItem(tx.ctx, db.SpendVirtualItemParams{CommanderID: tx.commanderID, ItemID: goods.ResourceType, Count: price})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotEnough
		}
		return nil
	}
	return errRefused
}

// boughtActivityGoods is how many times the commander bought goods of an
// activity shop.
func boughtActivityGoods(ctx context.Context, q *db.Queries, commanderID, activityID, goodsID uint32) (uint32, error) {
	rows, err := q.ListNewServerShopGoods(ctx, db.ListNewServerShopGoodsParams{CommanderID: commanderID, ActivityID: activityID})
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if row.GoodsID == goodsID {
			return row.Bought, nil
		}
	}
	return 0, nil
}

// metaShopLimits are the META shop goods' limit_args as the client reads them
// (ActivityCommodity:CheckArgLimit): the META ship group whose ownership the
// goods needs (ShopArgs.LIMIT_ARGS_META_SHIP_EXISTENCE, [1, group, 1]), the
// group whose ship must not be owned yet (LIMIT_ARGS_UNIQUE_SHIP,
// ["uniqueship", group]) and when it goes on sale (LIMIT_ARGS_SALE_START_TIME,
// [2, [y, m, d], [h, m, s]]).
type metaShopLimits struct {
	mustOwn uint32
	unique  uint32
	onSale  time.Time
}

// metaShopGoodsLimits reads the limit_args of a META shop goods.
func metaShopGoodsLimits(raw json.RawMessage) metaShopLimits {
	var entries [][]json.RawMessage
	if json.Unmarshal(raw, &entries) != nil {
		return metaShopLimits{}
	}
	var limits metaShopLimits
	for _, entry := range entries {
		if len(entry) < 2 {
			continue
		}
		var name string
		if json.Unmarshal(entry[0], &name) == nil {
			if name == "uniqueship" {
				_ = json.Unmarshal(entry[1], &limits.unique)
			}
			continue
		}
		var kind uint32
		if json.Unmarshal(entry[0], &kind) != nil {
			continue
		}
		switch kind {
		case 1:
			// [1, group, 1]: the META ship is needed (1 alone; 3 is the
			// exchange-instead-of-item case belfast has no goods of)
			var group, exchange uint32
			if len(entry) >= 3 && json.Unmarshal(entry[1], &group) == nil && json.Unmarshal(entry[2], &exchange) == nil && exchange == 1 {
				limits.mustOwn = group
			}
		case 2:
			var date, clock []uint32
			if len(entry) >= 3 && json.Unmarshal(entry[1], &date) == nil && json.Unmarshal(entry[2], &clock) == nil &&
				len(date) == 3 && len(clock) == 3 {
				limits.onSale = time.Date(int(date[0]), time.Month(date[1]), int(date[2]), int(clock[0]), int(clock[1]), int(clock[2]), 0, gamedata.Zone())
			}
		}
	}
	return limits
}

// metaShopOperation buys the META shop's goods (CS_11202, MetaShoppingCommand):
// arg1 the goods and arg2 how many its picks count, kvargs1 the picks (key the
// commodity, value how many of it), goods that don't let the commander choose
// sending their commodity_id. Each pick gives num of it, and one goods is
// limited to num_limit picks' worth (0 no limit). A ship already owned isn't
// bought again ("uniqueship"), and the exclusive goods need their META ship and
// sale date. The purchases are recorded like the other activity shops'
// (new_server_shop_goods), by the activity, and read back in data1_list /
// data2_list.
func (game *Game) metaShopOperation(client *connection.Client, config activityConfig, req *protobuf.CS_11202) error {
	var goodsIDs []uint32
	if json.Unmarshal(config.ConfigData, &goodsIDs) != nil || len(goodsIDs) == 0 {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	goodsID, count := req.GetArg1(), req.GetArg2()
	goods, ok := activityShop[goodsID]
	if !ok || !slices.Contains(goodsIDs, goodsID) || count == 0 || goods.Num == 0 {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	var drops []*protobuf.DROPINFO
	picked := uint32(0)
	for _, pick := range req.GetKvargs1() {
		id, number := pick.GetKey(), pick.GetValue()
		logger.WithFields("MetaShop",
			logger.FieldValue("goods", goodsID),
			logger.FieldValue("commodity", id),
			logger.FieldValue("number", number),
		).Debug("pick")
		if number == 0 || (id != goods.CommodityID && !slices.Contains(goods.CommodityIDList, id)) {
			return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
		}
		drops = append(drops, dropInfo(goods.CommodityType, id, goods.Num*number))
		picked += number
	}
	if len(drops) == 0 || picked != count {
		logger.WithFields("MetaShop",
			logger.FieldValue("goods", goodsID),
			logger.FieldValue("count", count),
			logger.FieldValue("picked", picked),
			logger.FieldValue("drops", len(drops)),
		).Warn("picks refused")
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	logger.WithFields("MetaShop",
		logger.FieldValue("activity", config.ID),
		logger.FieldValue("goods", goodsID),
		logger.FieldValue("count", count),
		logger.FieldValue("picks", len(drops)),
	).Info("buy")
	limits := metaShopGoodsLimits(goods.LimitArgs)
	reply := &protobuf.SC_11203{Result: result(nil)}
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		refuse := func(why string) error {
			logger.WithFields("MetaShop", logger.FieldValue("goods", goodsID), logger.FieldValue("why", why)).Warn("refused")
			return errRefused
		}
		if !limits.onSale.IsZero() && time.Now().Before(limits.onSale) {
			return refuse("not on sale")
		}
		// a META ship group is its template id without the last digit
		// (MetaCharacterConst.GetMetaShipGroupIDByConfigID)
		if limits.mustOwn != 0 {
			owned, err := tx.ownsMetaShipGroup(limits.mustOwn * 10)
			if err != nil {
				return err
			}
			if !owned {
				return refuse("meta ship missing")
			}
		}
		if limits.unique != 0 {
			owned, err := tx.ownsMetaShipGroup(limits.unique * 10)
			if err != nil {
				return err
			}
			if owned {
				return refuse("ship owned")
			}
		}
		if goods.NumLimit > 0 {
			bought, err := boughtActivityGoods(tx.ctx, tx.Queries, tx.commanderID, config.ID, goodsID)
			if err != nil {
				return err
			}
			if bought+count > goods.NumLimit {
				return refuse("limit")
			}
		}
		if err := tx.spendActivityGoods(goods, count); err != nil {
			logger.WithFields("MetaShop", logger.FieldValue("goods", goodsID), logger.FieldValue("error", err.Error())).Warn("spend failed")
			return err
		}
		reply.AwardList = drops
		if err := tx.grantDrops(drops); err != nil {
			return err
		}
		return tx.AddNewServerShopGoods(tx.ctx, db.AddNewServerShopGoodsParams{
			CommanderID: tx.commanderID, ActivityID: config.ID, GoodsID: goodsID, Bought: count,
		})
	})
	if refused(err) {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}
