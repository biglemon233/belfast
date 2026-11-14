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
	"github.com/ggmolly/belfast/internal/protobuf"
)

// ActivityConst.ACTIVITY_TYPE_NEWSERVER_SHOP and
// ACTIVITY_TYPE_BLACK_FRIDAY_SHOP: the shops of GetNewServerShopCommand
// (CS_26041), their goods the activity's config_data.
const (
	activityTypeNewServerShop   = 83  // newserver_shop_template
	activityTypeBlackFridayShop = 120 // blackfriday_shop_template
	// the new commander carnival's task page (NewServerTaskPage): its
	// activity's config_data are the task groups the page lists
	activityTypeNewServerTask = 82
)

// newServerTasks are the new commander carnival's tasks (NewServerTaskPage):
// the running new-server task activity's config_data groups, flattened. The
// page holds every one of them in TaskProxy (it asserts on each), so the
// server hands them all out at login.
func newServerTasks(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) ([]uint32, error) {
	own, err := newUserActivities(ctx, queries, commanderID, now)
	if err != nil {
		return nil, err
	}
	var ids []uint32
	for id := range own {
		config, ok := activityConfigs[id]
		if !ok || config.Type != activityTypeNewServerTask {
			continue
		}
		ids = append(ids, gamedata.IDs(config.ConfigData)...)
	}
	return ids, nil
}

// newServerGoods is a newserver_shop_template / blackfriday_shop_template
// row (NewServerCommodity, BlackFridayCommodity): num of each of goods
// (drop type Type) for ResourceNum - Discount of resource ResourceType,
// GoodsPurchaseLimit times. goods_type 2 and 4 let one pick which of goods
// (Selectable), 4 each of them once (LimitPurchaseSubGoods). A new
// commander shop's goods go on sale UnlockTime seconds after it opens
// (NewServerShop:IsOpenPhase).
type newServerGoods struct {
	Goods              []uint32 `json:"goods"`
	GoodsType          uint32   `json:"goods_type"`
	Type               uint32   `json:"type"`
	Num                uint32   `json:"num"`
	GoodsPurchaseLimit uint32   `json:"goods_purchase_limit"`
	ResourceCategory   uint32   `json:"resource_category"`
	ResourceType       uint32   `json:"resource_type"`
	ResourceNum        uint32   `json:"resource_num"`
	Discount           uint32   `json:"discount"`
	UnlockTime         uint32   `json:"unlock_time"`
}

// goods_type values
const (
	newServerGoodsSelectable = 2
	newServerGoodsOnceEach   = 4
)

var newServerShopGoods map[uint32]map[uint32]newServerGoods // by activity type

func loadNewServerShopData() error {
	var newServer, blackFriday map[uint32]newServerGoods
	err := gamedata.LoadAll(
		gamedata.Load("newserver_shop_template", &newServer),
		gamedata.Load("blackfriday_shop_template", &blackFriday),
	)
	if err != nil {
		return err
	}
	newServerShopGoods = map[uint32]map[uint32]newServerGoods{
		activityTypeNewServerShop:   newServer,
		activityTypeBlackFridayShop: blackFriday,
	}
	return nil
}

// newUserActivities are the new commander activities (activity_template
// time [["newuser", offset, days, since], ["timer", ...]]) the commander
// runs at now, with when each stops. Only the client's side of the window
// is known (Activity:getStartTime, model/vo/activity.lua: the start is the
// stop_time less days); the rest is a guess: a commander registered since
// `since` gets each of them from the server day it registered (plus offset
// days) for days days, while the timer runs.
func newUserActivities(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (map[uint32]uint32, error) {
	own := map[uint32]uint32{}
	var registered time.Time
	for id, config := range activityConfigs {
		if config.Time.NewUser == nil || now.Before(config.Time.Start) || !now.Before(config.Time.End) {
			continue
		}
		if registered.IsZero() {
			commander, err := queries.GetCommander(ctx, commanderID)
			if err != nil {
				return nil, err
			}
			registered = time.Unix(commander.RegisteredAt, 0)
		}
		start, stop, ok := config.Time.NewUser.Span(registered, config.Time.End)
		if ok && !now.Before(start) && now.Before(stop) {
			own[id] = uint32(stop.Unix())
		}
	}
	return own, nil
}

// shopWindow is when a new commander or Black Friday shop runs for the
// commander: its activity's newuser span, or its timer.
func shopWindow(ctx context.Context, queries *db.Queries, commanderID uint32, config activityConfig) (start, stop time.Time, err error) {
	start, stop = config.Time.Start, config.Time.End
	if config.Time.NewUser == nil {
		return start, stop, nil
	}
	commander, err := queries.GetCommander(ctx, commanderID)
	if err != nil {
		return start, stop, err
	}
	start, stop, _ = config.Time.NewUser.Span(time.Unix(commander.RegisteredAt, 0), config.Time.End)
	return start, stop, nil
}

// shopActivity is the running new commander or Black Friday shop activity
// id, with its goods.
func (game *Game) shopActivity(ctx context.Context, commanderID, id uint32, now time.Time) (activityConfig, []uint32, error) {
	config, open, err := game.commanderActivity(ctx, commanderID, id, now)
	if err != nil {
		return config, nil, err
	}
	if !open || newServerShopGoods[config.Type] == nil {
		return config, nil, errRefused
	}
	var goods []uint32
	if json.Unmarshal(config.ConfigData, &goods) != nil {
		return config, nil, errRefused
	}
	return config, goods, nil
}

// handleGetNewServerShop answers GetNewServerShopCommand
// (controller/command/shop/getnewservershopcommand.lua): the shop's span
// (the new commander shop opens its goods unlock_time seconds after
// start_time) and, for each of the activity's goods, the purchases left
// and the sub-goods already taken (NewServerCommodity). BlackFridayShop
// only knows the goods listed, so every goods is. No official capture has
// it: built from the Lua alone.
func (game *Game) handleGetNewServerShop(client *connection.Client, req *protobuf.CS_26041) error {
	ctx := context.Background()
	config, ids, err := game.shopActivity(ctx, client.CommanderID, req.GetActId(), time.Now())
	if refused(err) {
		return client.Send(&protobuf.SC_26042{Result: result(err), StartTime: proto.Uint32(0), StopTime: proto.Uint32(0)})
	}
	if err != nil {
		return err
	}
	start, stop, err := shopWindow(ctx, game.DB.Queries, client.CommanderID, config)
	if err != nil {
		return err
	}
	bought, err := game.DB.ListNewServerShopGoods(ctx, db.ListNewServerShopGoodsParams{CommanderID: client.CommanderID, ActivityID: config.ID})
	if err != nil {
		return err
	}
	subGoods, err := game.DB.ListNewServerShopSubGoods(ctx, db.ListNewServerShopSubGoodsParams{CommanderID: client.CommanderID, ActivityID: config.ID})
	if err != nil {
		return err
	}
	counts := map[uint32]uint32{}
	for _, row := range bought {
		counts[row.GoodsID] = row.Bought
	}
	records := map[uint32][]uint32{}
	for _, row := range subGoods {
		records[row.GoodsID] = append(records[row.GoodsID], row.ItemID)
	}
	table := newServerShopGoods[config.Type]
	reply := &protobuf.SC_26042{
		Result:    result(nil),
		StartTime: proto.Uint32(unixOrZero(start)),
		StopTime:  proto.Uint32(unixOrZero(stop)),
	}
	for _, id := range ids {
		goods, ok := table[id]
		if !ok {
			continue
		}
		reply.Goods = append(reply.Goods, &protobuf.ACT_GOODS_INFO{
			Id:           proto.Uint32(id),
			Count:        proto.Uint32(goods.GoodsPurchaseLimit - min(counts[id], goods.GoodsPurchaseLimit)),
			BoughtRecord: records[id],
		})
	}
	return client.Send(reply)
}

// unixOrZero is t as a protocol time, 0 for the zero time.
func unixOrZero(t time.Time) uint32 {
	if t.IsZero() {
		return 0
	}
	return uint32(t.Unix())
}

// handleNewServerShopShopping answers NewServerShopShoppingCommand
// (controller/command/shop/newservershopshoppingcommand.lua): selected are
// the goods picked, {itemid, count}. A goods one can't pick comes as its
// one item, count the times bought; a selectable one as the items picked,
// each count times, the times bought being their sum (each item once and
// never again for goods_type 4). On result 0 the client counts the
// purchases, records the sub-goods, takes resource_num (less the Black
// Friday discount) times the purchases off itself and adds drop_list.
// Like the client, the server checks the price (a player resource), the
// purchases left and, for the new commander shop, that the goods' phase is
// open (start + unlock_time). Built from the Lua alone.
func (game *Game) handleNewServerShopShopping(client *connection.Client, req *protobuf.CS_26043) error {
	ctx := context.Background()
	now := time.Now()
	var drops []*protobuf.DROPINFO
	config, ids, shopErr := game.shopActivity(ctx, client.CommanderID, req.GetActId(), now)
	if shopErr != nil && !refused(shopErr) {
		return shopErr
	}
	err := game.tx(ctx, client, func(tx *Tx) error {
		if shopErr != nil {
			return shopErr
		}
		id := req.GetGoodsid()
		goods, ok := newServerShopGoods[config.Type][id]
		if !ok || !slices.Contains(ids, id) || goods.ResourceCategory != dropTypeResource {
			return errRefused
		}
		if config.Type == activityTypeNewServerShop {
			start, _, err := shopWindow(ctx, tx.Queries, client.CommanderID, config)
			if err != nil {
				return err
			}
			if now.Before(start.Add(time.Duration(goods.UnlockTime) * time.Second)) {
				return errRefused
			}
		}
		times, picked, err := tx.newServerShopPick(config.ID, id, goods, req.GetSelected())
		drops = picked
		if err != nil {
			return err
		}
		rows, err := tx.ListNewServerShopGoods(ctx, db.ListNewServerShopGoodsParams{CommanderID: client.CommanderID, ActivityID: config.ID})
		if err != nil {
			return err
		}
		bought := uint32(0)
		for _, row := range rows {
			if row.GoodsID == id {
				bought = row.Bought
			}
		}
		if bought+times > goods.GoodsPurchaseLimit {
			return errRefused
		}
		price := goods.ResourceNum - min(goods.Discount, goods.ResourceNum)
		if err := tx.SpendShopResource(goods.ResourceType, price*times); err != nil {
			return err
		}
		if err := tx.grantDrops(drops); err != nil {
			return err
		}
		return tx.AddNewServerShopGoods(ctx, db.AddNewServerShopGoodsParams{CommanderID: client.CommanderID, ActivityID: config.ID, GoodsID: id, Bought: times})
	})
	if err != nil && !refused(err) {
		return err
	}
	if err != nil {
		drops = nil
	}
	return client.Send(&protobuf.SC_26044{Result: result(err), DropList: drops})
}

// newServerShopPick checks the goods picked and returns the times bought
// and what one gets: num of each item picked per time it is. It records
// the sub-goods taken of a goods_type 4.
func (tx *Tx) newServerShopPick(activityID, id uint32, goods newServerGoods, selected []*protobuf.ACT_GOODS_BUY) (uint32, []*protobuf.DROPINFO, error) {
	if len(goods.Goods) == 0 {
		return 0, nil, errRefused
	}
	if goods.GoodsType != newServerGoodsSelectable && goods.GoodsType != newServerGoodsOnceEach {
		if len(selected) != 1 || selected[0].GetItemid() != goods.Goods[0] || selected[0].GetCount() == 0 || selected[0].GetCount() > goods.GoodsPurchaseLimit {
			return 0, nil, errRefused
		}
		times := selected[0].GetCount()
		return times, []*protobuf.DROPINFO{dropInfo(goods.Type, goods.Goods[0], goods.Num*times)}, nil
	}
	times := uint32(0)
	var drops []*protobuf.DROPINFO
	for _, pick := range selected {
		item, count := pick.GetItemid(), pick.GetCount()
		if !slices.Contains(goods.Goods, item) || count == 0 || count > goods.GoodsPurchaseLimit {
			return 0, nil, errRefused
		}
		if goods.GoodsType == newServerGoodsOnceEach {
			if count != 1 {
				return 0, nil, errRefused
			}
			added, err := tx.AddNewServerShopSubGoods(tx.ctx, db.AddNewServerShopSubGoodsParams{CommanderID: tx.commanderID, ActivityID: activityID, GoodsID: id, ItemID: item})
			if err != nil {
				return 0, nil, err
			}
			if added == 0 {
				return 0, nil, errRefused
			}
		}
		drops = append(drops, dropInfo(goods.Type, item, goods.Num*count))
		times += count
	}
	if times == 0 {
		return 0, nil, errRefused
	}
	return times, drops, nil
}
