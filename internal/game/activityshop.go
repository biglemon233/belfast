package game

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

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

// shopRecords are the shop's bought counts as the page reads them: the flat
// [goods id, count] pairs of its data1_list, by goods id.
func shopRecords(ctx context.Context, q *db.Queries, commanderID, activityID uint32) ([]uint32, error) {
	rows, err := q.ListNewServerShopGoods(ctx, db.ListNewServerShopGoodsParams{CommanderID: commanderID, ActivityID: activityID})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b db.ListNewServerShopGoodsRow) int { return cmp.Compare(a.GoodsID, b.GoodsID) })
	list := make([]uint32, 0, len(rows)*2)
	for _, row := range rows {
		if row.Bought == 0 {
			continue
		}
		list = append(list, row.GoodsID, row.Bought)
	}
	return list, nil
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
		price := goods.ResourceNum * count
		switch goods.ResourceCategory {
		case dropTypeResource:
			if err := tx.SpendShopResource(goods.ResourceType, price); err != nil {
				return err
			}
		case dropTypeItem:
			if err := tx.SpendItem(goods.ResourceType, price); err != nil {
				return err
			}
		case dropTypeVItem:
			n, err := tx.SpendVirtualItem(tx.ctx, db.SpendVirtualItemParams{CommanderID: tx.commanderID, ItemID: goods.ResourceType, Count: price})
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrNotEnough
			}
		default:
			return errRefused
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
