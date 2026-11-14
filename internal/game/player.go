package game

import (
	"context"
	"maps"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// player_resource ids, next to ResourceGold / ResourceOil
const (
	ResourceOilField  = 5 // oil waiting in the academy's oil field
	ResourceGoldField = 7 // gold waiting in the academy's merchant
	// exp waiting in the academy's lecture hall (expField), packed into
	// EXP Data Packs by CS_22009
	ResourceClassField = 10
	ResourceGuildCoin  = 8
)

var (
	// starterFurniture is the backyard a new commander gets, one of each
	// furniture_init_data_template row (the "Run-down" theme, captured
	// SC_19001.furniture_id_list).
	starterFurniture []uint32
	// starterFurnitureTokens is what starterFurniture would cost in decor
	// tokens (furniture_shop_template.dorm_icon_price, 1 each), the
	// Renovation Merit Medal's starting progress (captured 14).
	starterFurnitureTokens uint32
)

// furniture_shop_template row
type furnitureShopConfig struct {
	DormIconPrice uint32 `json:"dorm_icon_price"`
}

func loadStarterFurniture() error {
	var initial map[uint32]struct{}
	var shop map[uint32]furnitureShopConfig
	err := gamedata.LoadAll(
		gamedata.Load("furniture_init_data_template", &initial),
		gamedata.Load("furniture_shop_template", &shop),
	)
	if err != nil {
		return err
	}
	starterFurniture = slices.Sorted(maps.Keys(initial))
	starterFurnitureTokens = 0
	for _, id := range starterFurniture {
		starterFurnitureTokens += shop[id].DormIconPrice
	}
	return nil
}

// grantPlayerStarter sets up what a new commander starts with besides ships:
// resources (new_commander.json), backyard furniture, the starter ship as
// secretary (the avatar is the starter ship).
// The capacities, rank, chat room... are column defaults (0001_commanders.sql).
func grantPlayerStarter(tx *Tx, commander db.Commander) error {
	ctx, queries := tx.ctx, tx.Queries
	for _, resource := range newCommander.Resources {
		if err := tx.AddResource(resource.ID, resource.Amount); err != nil {
			return err
		}
	}
	for _, id := range starterFurniture {
		if err := queries.AddFurniture(ctx, db.AddFurnitureParams{CommanderID: commander.ID, FurnitureID: id, Count: 1}); err != nil {
			return err
		}
	}
	ships, err := queries.ListShipsByCommander(ctx, commander.ID)
	if err != nil {
		return err
	}
	if ship, ok := starterShip(commander, ships); ok {
		return queries.SetSecretary(ctx, db.SetSecretaryParams{CommanderID: commander.ID, Position: 1, ShipID: ship.ID})
	}
	return nil
}

// starterShip finds the ship the commander picked at creation.
func starterShip(commander db.Commander, ships []db.Ship) (db.Ship, bool) {
	for _, ship := range ships {
		if ship.TemplateID == commander.StarterShip {
			return ship, true
		}
	}
	return db.Ship{}, false
}

// playerInfo is SC_11003, read by PlayerProxy into a Player (model/vo/player.lua).
func (game *Game) playerInfo(ctx context.Context, commander db.Commander, ships []db.Ship) (*protobuf.SC_11003, error) {
	stories, err := game.DB.ListStories(ctx, commander.ID)
	if err != nil {
		return nil, err
	}
	flags, err := game.DB.ListCommonFlags(ctx, commander.ID)
	if err != nil {
		return nil, err
	}
	if err := game.fillResourceFields(ctx, commander, time.Now()); err != nil {
		return nil, err
	}
	if err := game.DB.InTx(ctx, func(queries *db.Queries) error {
		return migrateResourceVirtuals(ctx, queries, commander.ID)
	}); err != nil {
		return nil, err
	}
	resources, err := game.DB.ListResources(ctx, commander.ID)
	if err != nil {
		return nil, err
	}
	secretaries, err := game.DB.ListSecretaries(ctx, commander.ID)
	if err != nil {
		return nil, err
	}
	randomShipMode, err := randomFlagshipMode(ctx, game.DB.Queries, commander.ID)
	if err != nil {
		return nil, err
	}
	cds, err := cooldowns(ctx, game.DB.Queries, commander.ID)
	if err != nil {
		return nil, err
	}
	purchases, err := shopPurchases(ctx, game.DB.Queries, commander.ID, time.Now())
	if err != nil {
		return nil, err
	}
	cryptolalia, err := game.DB.ListCryptolalia(ctx, commander.ID)
	if err != nil {
		return nil, err
	}
	guildWait, err := guildWaitTime(ctx, game.DB.Queries, commander.ID)
	if err != nil {
		return nil, err
	}
	owned, err := attireFrames(ctx, game.DB.Queries, commander.ID)
	if err != nil {
		return nil, err
	}
	look, err := appearance(ctx, game.DB.Queries, commander.ID)
	if err != nil {
		return nil, err
	}
	medals, err := displayMedals(ctx, game.DB.Queries, commander.ID)
	if err != nil {
		return nil, err
	}
	appreciation, mangaRead, mangaLiked, err := appreciationLogin(ctx, game.DB.Queries, commander.ID)
	if err != nil {
		return nil, err
	}
	payExp, err := chargeExp(ctx, game.DB.Queries, commander.ID)
	if err != nil {
		return nil, err
	}
	characters := make([]*protobuf.KVDATA, len(secretaries))
	for i, secretary := range secretaries {
		characters[i] = &protobuf.KVDATA{Key: proto.Uint32(secretary.ShipID), Value: proto.Uint32(secretary.PhantomID)}
	}
	return &protobuf.SC_11003{
		Id:                 proto.Uint32(commander.ID),
		Name:               proto.String(commander.Name),
		Level:              proto.Uint32(commander.Level),
		Exp:                proto.Uint32(commander.Exp),
		ResourceList:       resourceList(resources),
		Adv:                proto.String(commander.Manifesto),
		Character:          characters,
		ShipBagMax:         proto.Uint32(commander.ShipBagMax),
		EquipBagMax:        proto.Uint32(commander.EquipBagMax),
		Rank:               proto.Uint32(commander.Rank),
		GuideIndex:         proto.Uint32(commander.GuideIndex),
		ChatRoomId:         proto.Uint32(commander.ChatRoomID),
		RegisterTime:       proto.Uint32(uint32(commander.RegisteredAt)),
		ShipCount:          proto.Uint32(uint32(len(ships))),
		CommanderBagMax:    proto.Uint32(commander.CommanderBagMax),
		Display:            game.displayInfo(commander),
		MailStoreroomLv:    proto.Uint32(commander.MailStoreroomLv),
		BattleUiList:       owned.combatUIs,
		BattleUi:           proto.Uint32(look.BattleUi), // CS_11005 type 4
		Cover:              &protobuf.LIVINGAREA_COVER{Id: proto.Uint32(look.Cover), Covers: owned.covers},
		LoadingPicOpenFlag: proto.Uint32(boolFlag(look.LoadingPicOpen)),
		LoadingPicIdList_1: look.GalleryPics,
		LoadingPicIdList_2: look.MangaPics,
		NewGuideIndex:      proto.Uint32(commander.NewGuideIndex),
		StoryList:          stories,
		FlagList:           flags,
		BuyOilCount:        proto.Uint32(oilBought(purchases)),
		RandomShipMode:     proto.Uint32(randomShipMode),
		CdList:             cds,
		Soundstory:         cryptolalia,
		GuildWaitTime:      proto.Uint32(guildWait),
		IconFrameList:      owned.icons,
		ChatFrameList:      owned.chats,
		MarryShip:          proto.Uint32(commander.MarryShip),
		MedalId:            medals,
		Appreciation:       appreciation,
		CartoonReadMark:    mangaRead,
		CartoonCollectMark: mangaLiked,
		AccPayLv:           proto.Uint32(payExp),
		ChildDisplay:       proto.Uint32(commander.ChildDisplay),
	}, nil
}
