package game

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// item_data_statistics usage (ItemUsage, model/const/itemusage.lua) of the
// items belfast opens, with the skin ones of skinitems.go and the ship gifts
// of gifts.go, the guild orders of guildlogistics.go. The others change
// state belfast doesn't keep yet (commander boxes, remastered chapters) and
// are refused. Skill books,
// augment module stones and combat logistics plans are spent by their own
// packets, never through CS_15002 (UseItemCommand asserts on them).
const (
	usageDrop           = "usage_drop"
	usageDropTemplate   = "usgae_drop_template" // sic
	usageDropAppointed  = "usage_drop_appointed"
	usageInvitation     = "usage_invitation"
	usageFood           = "usage_food"
	usageDormLvUp       = "usage_dorm_lv_up"
	usageShipGift       = "usage_favorability"
	usageGuildDonate    = "usage_guild_donate"
	usageGuildOperation = "usage_guild_operation"
	usageBlueprint      = "usage_blueprint"
	// the cat box quick finisher (20010), used on a training box
	usageReduceCommanderTime = "usage_reduce_commander_time"
)

// itemUseConfig is what item_data_statistics says about using an item.
type itemUseConfig struct {
	Type        uint32          `json:"type"`
	Name        string          `json:"name"`
	Display     string          `json:"display"`
	Usage       string          `json:"usage"`
	UsageArg    json.RawMessage `json:"usage_arg"`
	DisplayIcon json.RawMessage `json:"display_icon"`
	// combining: compose_number of the item make one target_id
	ComposeNumber uint32 `json:"compose_number"`
	TargetID      uint32 `json:"target_id"`
}

// itemUse is an item_data_statistics usage with its usage_arg read.
type itemUse struct {
	usage string
	// usage_drop, usgae_drop_template: what one item gives. The drop
	// tables are server-side; display_icon lists the contents the client
	// shows, and a template's usage_arg [drop, gold, oil] its resources
	// (Item:GetOverflowCheckItems, model/vo/item.lua).
	contents [][]uint32
	// usage_drop tech packs without a drop_packs.json entry: the fallback
	// draw
	pack *dropPack
	// usage_drop_appointed: the [type, id, count] entries one is picked
	// from (AssignedItemView, view/equipment/assigneditemview.lua).
	choices [][]uint32
	// usage_invitation: the item_usage_invitation ids one is picked from
	// (BaseAssignedShipScene, view/activity/assignedship/).
	invitations []uint32
	// usage_food: the food one item adds to the backyard (AddFoodCommand)
	food uint32
	// usage_favorability: the affection one gift gives (giveShipGift)
	affection uint32
	// the skin items' usage_arg (skinitems.go)
	skin skinUse
	// usage_blueprint: the development exp one research ship blueprint
	// gives (ShipBluePrint:getItemExp, usage_arg[1])
	blueprintExp uint32
}

var itemUses map[uint32]itemUse

// techBoxItems are the tech packs (item_data_statistics type 5): the commander
// manual's task 22015 counts how many are opened.
var techBoxItems map[uint32]bool

// item_usage_invitation row: the ship an invitation gives, and the
// commemorative item that comes with it (0 none)
type invitationConfig struct {
	ShipID uint32 `json:"ship_id"`
	CoinID uint32 `json:"coin_id"`
}

var invitations map[uint32]invitationConfig

func loadItemData() error {
	return gamedata.LoadAll(
		gamedata.Load("item_usage_invitation", &invitations),
		loadDropPacks,
	)
}

// itemTypeEquipBox is Item.EQUIPMENT_BOX_TYPE_5 (model/vo/item.lua), the
// tech packs (the client checks the depot has room for count gear).
const itemTypeEquipBox = 5

// packGroup is one outcome of a random pack: drawn by weight, then one of
// its pool's [type, id, count] entries; an empty pool gives nothing.
type packGroup struct {
	Weight uint32     `json:"weight"`
	Pool   [][]uint32 `json:"pool"`
}

// dropPack is a random pack's draw: its fixed [type, id, count] entries,
// then one of its groups.
type dropPack struct {
	Fixed  [][]uint32  `json:"fixed"`
	Groups []packGroup `json:"groups"`
}

// dropPacks are what the packs whose drop tables are server-side hold, by
// item_data_statistics or item_virtual_data_statistics id: the tech packs,
// the gear development packs, the "Mystery" placeholders the gift packs'
// display_icon lists (54033 Mystery T3 Tech Pack...), the guild supplies
// (guildset guild_award_drop). Server content from server/drop_packs.json
// of the data dir ({"<id>": {"fixed": [...], "groups": [...]}}),
// scraped from the wiki's equipment box table by
// internal/tools/wiki_drop_packs.py. Without it the nation tech packs fall
// back to what their item text says (readTechPack).
var dropPacks map[uint32]dropPack

func loadDropPacks() error {
	dropPacks = map[uint32]dropPack{}
	raw, err := gamedata.ServerFile("drop_packs.json")
	if raw == nil || err != nil {
		return err
	}
	var rows map[uint32]dropPack
	if err := json.Unmarshal(raw, &rows); err != nil {
		return fmt.Errorf("drop packs: %w", err)
	}
	for id, row := range rows {
		if row.valid() {
			dropPacks[id] = row
		}
	}
	return nil
}

func (pack dropPack) valid() bool {
	short := func(entry []uint32) bool { return len(entry) < 3 }
	return (len(pack.Fixed) > 0 || len(pack.Groups) > 0) && !slices.ContainsFunc(pack.Fixed, short) &&
		!slices.ContainsFunc(pack.Groups, func(group packGroup) bool {
			return group.Weight == 0 || slices.ContainsFunc(group.Pool, short)
		})
}

// draw draws the pack count times, merging repeats.
func (pack dropPack) draw(count uint32) []*protobuf.DROPINFO {
	var drops []*protobuf.DROPINFO
	for range count {
		for _, entry := range pack.Fixed {
			drops = mergeDrop(drops, entry[0], entry[1], entry[2])
		}
		if len(pack.Groups) == 0 {
			continue
		}
		i := weightedPick(rand.Uint32N, pack.Groups, func(group packGroup) uint32 { return group.Weight })
		if i < 0 || len(pack.Groups[i].Pool) == 0 {
			continue
		}
		entry := pack.Groups[i].Pool[rand.IntN(len(pack.Groups[i].Pool))]
		drops = mergeDrop(drops, entry[0], entry[1], entry[2])
	}
	return drops
}

// mergeDrop adds a reward to drops, to its entry if it has one (ships
// excepted: each is its own).
func mergeDrop(drops []*protobuf.DROPINFO, kind, id, number uint32) []*protobuf.DROPINFO {
	if kind != dropTypeShip {
		if i := slices.IndexFunc(drops, func(drop *protobuf.DROPINFO) bool { return drop.GetType() == kind && drop.GetId() == id }); i >= 0 {
			drops[i].Number = proto.Uint32(drops[i].GetNumber() + number)
			return drops
		}
	}
	return append(drops, dropInfo(kind, id, number))
}

// packGear is a piece of gear a tech pack can hold: a +0 equip_data_template
// whose statistics have a grade (tech 1-3).
type packGear struct {
	id, nationality, rarity uint32
}

func techPackGear(templates map[uint32]equipTemplateConfig, stats map[uint32]equipStatisticsConfig) []packGear {
	var gear []packGear
	for id, template := range templates {
		stat, ok := stats[id]
		if ok && template.Prev == 0 && stat.Tech > 0 {
			gear = append(gear, packGear{id, stat.Nationality, stat.Rarity})
		}
	}
	slices.SortFunc(gear, func(a, b packGear) int { return cmp.Compare(a.id, b.id) })
	return gear
}

// techPackNation is a tech pack's nation (Nation, model/const/nation.lua):
// the packs are numbered 300<nation><tier> (30011 T1 Eagle to 30045 T5 Iron
// Blood, as their names say in every region), and a pack outside that
// numbering (30206, a T3 Sakura display sample) is of the numbered pack
// whose name, in the region's own text, its name contains. 0 when neither
// says (the starter submarine box, the gear development packs).
func techPackNation(id uint32, row itemConfig, items map[uint32]itemConfig) uint32 {
	if nation, tier := id/10%10, id%10; id/100 == 300 && nation > 0 && tier > 0 {
		return nation
	}
	nationality, found := uint32(0), 0
	for other, pack := range items {
		if other/100 == 300 && other/10%10 > 0 && other%10 > 0 && pack.Name != "" && strings.Contains(row.Name, pack.Name) {
			nationality, found = other/10%10, found+1
		}
	}
	if found != 1 {
		return 0
	}
	return nationality
}

// packRate and packColor read a tech pack's display lines, worded in the
// region's language: "40% chance to receive <color=#3dc6ff>Rare
// gear</color>.", "40%几率获得<color=#3dc6ff>稀有装备</color>".
var (
	packRate  = regexp.MustCompile(`(\d+)\s*%`)
	packColor = regexp.MustCompile(`<color=#([0-9A-Fa-f]{6})>`)
)

// gearRarityByColor are the gear colors by rarity of
// RecordShipEquipmentCommand (controller/command/ship/
// recordshipequipmentcommand.lua, indexed by rarity - 1), the other palette
// the pack texts color their gear with (TW's all, some of EN's); the rest
// use rarityByRateColor's.
var gearRarityByColor = map[string]uint32{"60a9ff": 3, "966af6": 4, "fff157": 5}

// techPackOutcomes reads a tech pack's draw from its display text without
// its words: after the first line, one line per outcome, its weight the
// rate on it, its rarity the color of its gear (Common uncolored); the
// last line of a pack that combines (compose_number) is the hint saying
// so. A single outcome may have no rate; several without one (TW's
// "較低機率獲得...", lower chance) can't be read: nil.
func techPackOutcomes(row itemConfig) [][2]uint32 {
	var lines []string
	for _, line := range strings.Split(row.Display, "\n")[1:] {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if n := len(lines); n > 0 && row.ComposeNumber > 0 {
		lines = lines[:n-1]
	}
	var outcomes [][2]uint32
	for _, line := range lines {
		rarity := uint32(shipRarityGray) // gear rarities count as ships'
		if match := packColor.FindStringSubmatch(line); match != nil {
			color := strings.ToLower(match[1])
			found, ok := rarityByRateColor[color]
			if !ok {
				found, ok = gearRarityByColor[color]
			}
			if !ok {
				return nil
			}
			rarity = found
		}
		weight := uint64(100) // the only outcome
		if match := packRate.FindStringSubmatch(line); match != nil {
			weight, _ = strconv.ParseUint(match[1], 10, 32)
		} else if len(lines) > 1 {
			return nil
		}
		outcomes = append(outcomes, [2]uint32{uint32(weight), rarity})
	}
	return outcomes
}

// readTechPack is the fallback draw of a tech pack, from what the client
// shows of it: a rarity by the outcomes of its display text, then any
// graded +0 gear of that rarity of its nation. nil when either can't be
// read.
func readTechPack(id uint32, row itemConfig, gear []packGear, items map[uint32]itemConfig) *dropPack {
	nationality := techPackNation(id, row, items)
	if nationality == 0 {
		return nil
	}
	byRarity := map[uint32][][]uint32{}
	for _, piece := range gear {
		if piece.nationality == nationality {
			byRarity[piece.rarity] = append(byRarity[piece.rarity], []uint32{dropTypeEquip, piece.id, 1})
		}
	}
	var pack dropPack
	for _, outcome := range techPackOutcomes(row) {
		pack.Groups = append(pack.Groups, packGroup{outcome[0], byRarity[outcome[1]]})
	}
	// a rate whose rarity has no gear is a misread: no pack rather than
	// one that may give nothing
	if !pack.valid() || slices.ContainsFunc(pack.Groups, func(group packGroup) bool { return len(group.Pool) == 0 }) {
		return nil
	}
	return &pack
}

// readItemUses reads the usage of the item_data_statistics rows belfast
// opens.
func readItemUses(items map[uint32]itemConfig, gear []packGear) {
	itemUses = make(map[uint32]itemUse, len(items))
	techBoxItems = map[uint32]bool{}
	itemComposes = map[uint32]itemCompose{}
	var unread []uint32 // tech packs whose text gives no draw
	for id, row := range items {
		if row.Type == itemTypeEquipBox {
			techBoxItems[id] = true
		}
		readItemCompose(id, row.itemUseConfig)
		use := itemUse{usage: row.Usage}
		var icons, args [][]uint32
		_ = json.Unmarshal(row.DisplayIcon, &icons)
		switch row.Usage {
		case usageDrop:
			use.contents = icons
			if len(icons) == 0 && row.Type == itemTypeEquipBox {
				use.pack = readTechPack(id, row, gear, items)
				if use.pack == nil && techPackNation(id, row, items) != 0 {
					unread = append(unread, id)
				}
			}
		case usageDropTemplate:
			var template []uint32
			if json.Unmarshal(row.UsageArg, &template) != nil || len(template) < 3 {
				break
			}
			if template[1] > 0 {
				use.contents = append(use.contents, []uint32{dropTypeResource, ResourceGold, template[1]})
			}
			if template[2] > 0 {
				use.contents = append(use.contents, []uint32{dropTypeResource, ResourceOil, template[2]})
			}
			// display_icon shows the template's gold and oil too
			for _, icon := range icons {
				if len(icon) >= 2 && icon[0] == dropTypeResource && (icon[1] == ResourceGold || icon[1] == ResourceOil) {
					continue
				}
				use.contents = append(use.contents, icon)
			}
		case usageDropAppointed:
			if json.Unmarshal(row.UsageArg, &args) == nil {
				use.choices = args
			}
		case usageInvitation:
			use.invitations = gamedata.IDs(row.UsageArg)
		case usageFood:
			if food := gamedata.IDs(row.UsageArg); len(food) > 0 {
				use.food = food[0]
			}
		case usageShipGift:
			use.affection = readShipGift(row.UsageArg)
		case usageBlueprint:
			if exp := gamedata.IDs(row.UsageArg); len(exp) > 0 {
				use.blueprintExp = exp[0]
			}
		case usageRandomSkin, usageSkinSelect, usageSkinDiscount, usageSkinExp:
			use.skin = readSkinUse(row, icons, items)
		}
		itemUses[id] = use
	}
	if len(unread) > 0 {
		slices.Sort(unread)
		logger.WithFields("Items", logger.FieldValue("packs", unread)).Info("tech packs whose text gives no rates: drop_packs.json is their only draw")
	}
}

// storableDrop is whether grantDrops keeps a reward of an item's contents:
// resources, bag items, equipment, ships (any ownableShip), ship and gear
// skins and meowfficers.
// display_icon also lists placeholders for what the server draws (the
// "Mystery" virtual items) and kinds belfast doesn't store (furniture,
// frames...).
func storableDrop(kind, id uint32) bool {
	switch kind {
	case dropTypeResource:
		return true
	case dropTypeItem:
		_, ok := itemUses[id]
		return ok
	case dropTypeEquip:
		return equipIDs[id]
	case dropTypeShip:
		_, ok := ownableShip(id)
		return ok
	case dropTypeSkin, dropTypeSkinTimeLimit:
		// the client shows a skin on its group's star-1 ship
		// (Drop.UpdateDropCase: ship_group .. "1"), which must exist
		skin, ok := shipSkins[id]
		_, base := ownableShip(skin.group*10 + 1)
		return ok && skin.words && base
	case dropTypeEquipSkin:
		_, ok := equipSkins[id]
		return ok
	case dropTypeMeowfficer:
		_, ok := meowfficerTemplates[id]
		return ok
	}
	return false
}

// handleUseItem answers UseItemCommand and UseFudaiItemCommand
// (controller/command/item/), using count of a bag item, arg naming the pick
// of a selection box. On result 0 the client removes the items itself and,
// for the boxes and invitations, adds drop_list (PlayerConst.addTranDrop);
// for food it adds usage_arg[1] per item to the backyard itself
// (AddFoodCommand) and ignores drop_list. No official capture has it: the
// reply is built from the Lua alone.
func (game *Game) handleUseItem(client *connection.Client, req *protobuf.CS_15002) error {
	ctx := context.Background()
	var drops []*protobuf.DROPINFO
	err := game.tx(ctx, client, func(tx *Tx) (err error) {
		drops, err = tx.useItem(req.GetId(), req.GetCount(), req.GetArg())
		return err
	})
	if err != nil && !refused(err) {
		return err
	}
	if err != nil {
		drops = nil
	}
	return client.Send(&protobuf.SC_15003{Result: result(err), DropList: drops})
}

// useItem spends count of an item and gives what it holds, returning the
// drops given. Nothing is spent when it can't be opened.
func (tx *Tx) useItem(id, count uint32, arg []uint32) ([]*protobuf.DROPINFO, error) {
	use, ok := itemUses[id]
	if !ok || count == 0 {
		return nil, errRefused
	}
	var drops []*protobuf.DROPINFO
	switch use.usage {
	case usageDrop, usageDropTemplate:
		if pack, ok := dropPacks[id]; ok {
			drops = pack.draw(count)
			break
		}
		if use.pack != nil {
			drops = use.pack.draw(count)
			break
		}
		if len(use.contents) == 0 {
			return nil, errRefused
		}
		for _, entry := range use.contents {
			if len(entry) < 3 {
				return nil, errRefused
			}
			drops = resolveContent(drops, entry[0], entry[1], entry[2]*count)
		}
	case usageDropAppointed:
		// arg is the picked usage_arg entry
		i := slices.IndexFunc(use.choices, func(choice []uint32) bool { return slices.Equal(choice, arg) })
		if i < 0 || len(arg) < 3 {
			return nil, errRefused
		}
		drops = []*protobuf.DROPINFO{dropInfo(arg[0], arg[1], arg[2]*count)}
	case usageInvitation:
		// arg is the picked item_usage_invitation id
		if len(arg) != 1 || !slices.Contains(use.invitations, arg[0]) {
			return nil, errRefused
		}
		invitation, ok := invitations[arg[0]]
		if !ok {
			return nil, errRefused
		}
		drops = []*protobuf.DROPINFO{dropInfo(dropTypeShip, invitation.ShipID, count)}
		if invitation.CoinID != 0 {
			drops = append(drops, dropInfo(dropTypeItem, invitation.CoinID, count))
		}
	case usageFood:
		if use.food == 0 {
			return nil, errRefused
		}
		if err := tx.SpendItem(id, count); err != nil {
			return nil, err
		}
		// the event tasks count the food added ("后宅补充食物5次")
		tx.event(TaskEvent{SubType: TaskSubDormFood, Count: count})
		return nil, tx.addDormFood(use.food * count)
	case usageDormLvUp:
		// ExtendBackYardAreaCommand: one level per use
		if count != 1 {
			return nil, errRefused
		}
		if err := tx.SpendItem(id, count); err != nil {
			return nil, err
		}
		return nil, tx.expandDorm()
	case usageShipGift:
		// arg is the ship given to
		return nil, tx.giveShipGift(id, count, use.affection, arg)
	case usageRandomSkin, usageSkinSelect, usageSkinDiscount:
		skins, err := tx.skinItemDrops(count, arg, use)
		if err != nil {
			return nil, err
		}
		drops = skins
	case usageSkinExp:
		return nil, tx.useSkinTrial(id, count, arg, use)
	case usageGuildDonate, usageGuildOperation:
		return nil, tx.useGuildOrders(id, count, use.usage)
	case usageReduceCommanderTime:
		// the cat box quick finisher: arg is the box it shortens
		return nil, tx.quickFinishMeowfficerBox(arg, count)
	default:
		return nil, errRefused
	}
	if len(drops) == 0 {
		return nil, errRefused
	}
	for _, drop := range drops {
		if !storableDrop(drop.GetType(), drop.GetId()) {
			return nil, errRefused
		}
	}
	if err := tx.SpendItem(id, count); err != nil {
		return nil, err
	}
	if techBoxItems[id] {
		tx.event(TaskEvent{SubType: TaskSubTechBox, Count: count})
	}
	return drops, tx.grantDrops(drops)
}

// resolveContent adds number of a pack's content to drops. A display_icon
// item entry that isn't an item is a placeholder for what the server draws:
// a "Mystery" one of dropPacks, or a container virtual item whose own
// display_icon lists what it turns into (dropPools: 150001 T2 Development
// Materials Container...), drawn once per unit. What can't be resolved is
// left as is, for storableDrop to refuse.
func resolveContent(drops []*protobuf.DROPINFO, kind, id, number uint32) []*protobuf.DROPINFO {
	if _, item := itemUses[id]; kind != dropTypeItem || item {
		return mergeDrop(drops, kind, id, number)
	}
	if pack, ok := dropPacks[id]; ok {
		for _, drop := range pack.draw(number) {
			drops = mergeDrop(drops, drop.GetType(), drop.GetId(), drop.GetNumber())
		}
		return drops
	}
	if pool := dropPools[id]; len(pool) > 0 {
		for range number {
			ref := pool[rand.IntN(len(pool))]
			drops = mergeDrop(drops, ref.kind, ref.id, 1)
		}
		return drops
	}
	return mergeDrop(drops, kind, id, number)
}
