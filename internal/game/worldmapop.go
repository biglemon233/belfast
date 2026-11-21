package game

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"math/rand/v2"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// Operation Siren's map operations: CS_33103 (WorldMapOpCommand) and its
// SC_33104. Built from the client's Lua and the world_* tables alone (the
// mode was never captured): controller/command/world/worldmapopcommand.lua
// (what the reply's fields become), mod/experiment/world/view/wscommand.lua
// (which op is sent when, with what), mod/experiment/world/model/
// worldmapop.lua (what the client changes itself once the op is applied,
// mirrored here so a reload shows the same world). Check on the device
// first.

// CS_33103.act: WorldConst.ReqName's indices (OpReq*).
const (
	worldReqMove        = 1  // a fleet to act_arg_1, act_arg_2
	worldReqBox         = 2  // open the box under the fleet
	worldReqRound       = 8  // the enemies' turn after the player's
	worldReqSub         = 9  // the submarine support order
	worldReqEvent       = 10 // run an event's next effect, pos_list its cell
	worldReqDiscover    = 12 // pos_list come into sight
	worldReqTransport   = 13 // to map act_arg_1 through entrance act_arg_2
	worldReqRetreat     = 14 // the fleet off the enemy it lost to
	worldReqTask        = 18 // the port's arrive-port tasks (WorldPortMediator)
	worldReqMaintenance = 20 // the maintenance order: every fleet repaired
	worldReqVision      = 21 // the detection order: the map's attachments shown
	worldReqPressing    = 25 // act_arg_1 secured (no infection left)
	worldReqJumpOut     = 26 // leave a world_chapter_template_reset map
	worldReqEnterPort   = 27
	worldReqCatSalvage  = 28 // a meowfficer salvage's result
	worldReqSwitchFleet = 29 // the active fleet becomes group_id
	worldReqSkipBattle  = 99 // a GM's win without the battle
)

// errWorldStamina is result 130, "world_stamina_not_enough" (and the
// stamina panel); the other refusals show errorTip("world_map_op_error_").
var errWorldStamina error = refusal{"not enough action points", 130}

// worldHappenings is what a map operation or a world battle did, for the
// achievements and tasks (phase 4 hooks: worldTargetsUpdated,
// worldTasksUpdated).
type worldHappenings struct {
	Steps     uint32   // cells the fleets moved
	Events    []uint32 // world_event_data ids that ran an effect
	Effects   []uint32 // world_effect_data ids run (branches resolved)
	Triggers  []uint32 // EffectEventTrigger (type 21) ids
	Accepted  []uint32 // world_task_data ids an effect type 5 hands out
	Deleted   []uint32 // world tasks an EffectEventDeleteTask removed
	Kills     []uint32 // enemies beaten (expedition ids)
	Losses    []uint32 // enemies lost to
	Boxes     []uint32 // box_data_template ids opened
	Pressed   []uint32 // maps secured
	Entered   []uint32 // maps entered (random ids)
	Salvaged  []uint32 // fleets whose meowfficer salvage ended
	PortTasks bool     // OpReqTask: the arrive-port tasks to check
}

// worldOp is a map operation under way.
type worldOp struct {
	tx    *Tx
	now   time.Time
	state *worldState
	// m is the active map (the destination once a transfer is done)
	m     *worldMap
	fleet *worldFleet // group_id's, nil for none
	reply *protobuf.SC_33104
	rng   *rand.Rand

	cells    [][2]uint32 // cells of m that changed (pos_list)
	lands    [][2]uint32 // cells of m whose terrain changed (land_list)
	ships    []uint32    // ships whose hp or buffs changed (ship_update)
	groups   []uint32    // fleets whose buffs changed (group_update)
	salvages []uint32    // fleets whose salvage changed (cmd_collection_list)
	drops    []*protobuf.DROPINFO
	left     []*worldMap // maps left behind, saved too
	cleared  []uint32    // maps left that start over (WorldMap:NeedClear)
	happened worldHappenings
}

// handleWorldMapOp answers WorldMapOpCommand (CS_33103): one operation on the
// active map, act a WorldConst.OpReq*. The reply is read whatever the op:
// drop_list (PlayerConst.addTranDrop: world items into the world
// inventory), pos_list (every cell that changed, whole: WorldProxy drops
// what a cell had that isn't listed; fleets as TypeFleet items), land_list,
// group_update (fleet buffs), ship_update (hp), cmd_collection_list,
// target_list; then per op: move_path, ai_act_list (round, retreat), the
// transfer fields (enter_map_id, id, action_power and _extra: the client
// sets its action points to them). Result 130 is "not enough action
// points".
func (game *Game) handleWorldMapOp(client *connection.Client, req *protobuf.CS_33103) error {
	ctx := context.Background()
	now := serverNow()
	var reply *protobuf.SC_33104
	err := game.tx(ctx, client, func(tx *Tx) error {
		state, err := tx.worldState(now)
		if err != nil {
			return err
		}
		if !state.activated() {
			return errRefused
		}
		m, err := tx.worldMap(state, state.MapID)
		if err != nil {
			return err
		}
		if m == nil || worldTemplates[m.TemplateID] == nil {
			return errRefused
		}
		// worlds activated before the rewards were handed out
		assignPressingAwards(state, tx.commanderID)
		op := &worldOp{
			tx:    tx,
			now:   now,
			state: state,
			m:     m,
			fleet: state.fleet(req.GetGroupId()),
			reply: &protobuf.SC_33104{Result: result(nil), EventId: proto.Uint32(0)},
			rng:   rand.New(rand.NewPCG(uint64(now.UnixNano()), uint64(tx.commanderID))),
		}
		if err := op.run(req); err != nil {
			return err
		}
		if err := op.finish(); err != nil {
			return err
		}
		reply = op.reply
		return nil
	})
	if refused(err) {
		return client.Send(&protobuf.SC_33104{Result: result(err), EventId: proto.Uint32(0)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}

func (op *worldOp) run(req *protobuf.CS_33103) error {
	cell := func() ([2]uint32, bool) {
		if len(req.GetPosList()) == 0 {
			return [2]uint32{}, false
		}
		pos := req.GetPosList()[0]
		return [2]uint32{pos.GetRow(), pos.GetColumn()}, true
	}
	switch req.GetAct() {
	case worldReqMove:
		return op.move(req.GetActArg_1(), req.GetActArg_2())
	case worldReqBox:
		return op.openBox()
	case worldReqRound:
		return op.round()
	case worldReqSub:
		return op.submarine()
	case worldReqEvent:
		pos, ok := cell()
		if !ok {
			return errRefused
		}
		return op.event(pos, req.GetActArg_1())
	case worldReqDiscover:
		return op.discover(req.GetPosList())
	case worldReqTransport:
		pos, ok := cell()
		return op.transport(req.GetActArg_1(), req.GetActArg_2(), pos, ok)
	case worldReqRetreat:
		return op.retreat()
	case worldReqTask:
		op.happened.PortTasks = true
		return nil
	case worldReqMaintenance:
		return op.maintenance()
	case worldReqVision:
		return op.vision()
	case worldReqPressing:
		return op.pressing(req.GetActArg_1())
	case worldReqJumpOut:
		return op.jumpOut()
	case worldReqEnterPort:
		if worldTemplates[op.m.TemplateID].port == 0 {
			return errRefused
		}
		return nil
	case worldReqCatSalvage:
		return op.catSalvage()
	case worldReqSwitchFleet:
		if op.fleet == nil || op.fleet.Submarine {
			return errRefused
		}
		return nil
	case worldReqSkipBattle:
		// World:CheckSkipBattle: an identity-flagged (GM) player with
		// world_skip_battle set; nobody is one here, and the op would be a
		// win (drops, achievements) without a battle
		return errRefused
	}
	return errRefused
}

// finish grants the drops, fills the reply's updates and saves the world.
func (op *worldOp) finish() error {
	state, tx := op.state, op.tx
	var drops []*protobuf.DROPINFO
	for _, drop := range op.drops {
		// DROP_TYPE_WORLD_COLLECTION: WorldCollectionProxy:Unlock
		if drop.GetType() == dropTypeWorldCollection {
			if !slices.Contains(state.Count.Collections, drop.GetId()) {
				state.Count.Collections = append(state.Count.Collections, drop.GetId())
			}
			continue
		}
		drops = append(drops, drop)
	}
	if err := tx.grantWorldDrops(state, drops); err != nil {
		return err
	}
	if exit := obscuredExit(op.m, op.rng); exit != nil {
		op.mark([2]uint32{exit.Row, exit.Column})
	}
	op.strongholdStep()
	reply := op.reply
	reply.DropList = op.drops
	for _, cell := range op.cells {
		reply.PosList = append(reply.PosList, op.m.posInfo(cell[0], cell[1], state.Fleets))
	}
	for _, cell := range op.lands {
		reply.LandList = append(reply.LandList, op.m.landInfo(cell))
	}
	for _, id := range op.ships {
		if ship := state.worldShip(id); ship != nil {
			reply.ShipUpdate = append(reply.ShipUpdate, ship.shipInfo())
		}
	}
	for _, id := range op.groups {
		if fleet := state.fleet(id); fleet != nil {
			reply.GroupUpdate = append(reply.GroupUpdate, &protobuf.GROUPINFOUPDATE{Id: proto.Uint32(id), BuffList: worldBuffInfos(fleet.Buffs)})
		}
	}
	for _, id := range op.salvages {
		if fleet := state.fleet(id); fleet != nil {
			reply.CmdCollectionList = append(reply.CmdCollectionList, &protobuf.GROUPCMDCOLLECTION{GroupId: proto.Uint32(id), CmdCollection: fleet.groupInfo().GetCmdCollection()})
		}
	}
	// only the transfers read them (BuildTransfer: ChangeStamina sets them)
	reply.ActionPower = proto.Uint32(state.Stamina.Power)
	reply.ActionPowerExtra = proto.Uint32(state.Stamina.Extra)
	reply.TargetList = tx.worldTargetsUpdated(state, &op.happened)
	if err := tx.worldTasksUpdated(state, &op.happened); err != nil {
		return err
	}
	for _, m := range op.left {
		if slices.Contains(op.cleared, m.RandomID) {
			if err := tx.DeleteWorldMap(tx.ctx, db.DeleteWorldMapParams{CommanderID: tx.commanderID, RandomID: m.RandomID}); err != nil {
				return err
			}
			continue
		}
		if err := tx.saveWorldMap(m); err != nil {
			return err
		}
	}
	if err := tx.saveWorldMap(op.m); err != nil {
		return err
	}
	return tx.saveWorldState(state)
}

// mark notes that a cell of the active map changed (pos_list).
func (op *worldOp) mark(cells ...[2]uint32) {
	for _, cell := range cells {
		if !slices.Contains(op.cells, cell) {
			op.cells = append(op.cells, cell)
		}
	}
}

func (op *worldOp) markShip(id uint32) {
	if !slices.Contains(op.ships, id) {
		op.ships = append(op.ships, id)
	}
}

func (op *worldOp) markFleetShips(fleet *worldFleet) {
	for _, ship := range fleet.Ships {
		op.markShip(ship.ID)
	}
}

func fleetCell(fleet *worldFleet) [2]uint32 {
	return [2]uint32{fleet.Row, fleet.Column}
}

// normalFleet is the acting fleet when it is a normal one (the ones on the
// map's cells).
func (op *worldOp) normalFleet() (*worldFleet, error) {
	if op.fleet == nil || op.fleet.Submarine {
		return nil, errRefused
	}
	return op.fleet, nil
}

// lastCD is when the order of WorldConst id was last given (cd_list).
func (state *worldState) lastCD(id uint32) int64 {
	for _, cd := range state.CDs {
		if cd.ID == id {
			return int64(cd.Count)
		}
	}
	return 0
}

// setCD starts the cooldown of an order (World:SetReqCDTime).
func (state *worldState) setCD(id uint32, now time.Time) {
	for i := range state.CDs {
		if state.CDs[i].ID == id {
			state.CDs[i].Count = uint32(now.Unix())
			return
		}
	}
	state.CDs = append(state.CDs, worldCount{ID: id, Count: uint32(now.Unix())})
}

// order pays an order's action points (the client takes the same,
// World:CalcOrderCost, ConsumeStamina) and starts its cooldown.
func (op *worldOp) order(id, cost uint32) error {
	if !op.state.Stamina.consume(cost) {
		return errWorldStamina
	}
	op.state.setCD(id, op.now)
	return nil
}

// move is OpReqMoveFleet: the fleet goes to row, column by the shortest way
// (WorldMap:FindPath: through walkable cells nothing blocks, to a cell it can
// stop on), which is move_path; a stream, then ice, carries it on. It
// counts the steps (step_count) and ends the player's turn (the client's
// IncRound, the round op follows). The cells it comes to see are
// discovered (the client sends them too, OpReqDiscover).
func (op *worldOp) move(row, column uint32) error {
	fleet, err := op.normalFleet()
	if err != nil {
		return err
	}
	from, to := fleetCell(fleet), [2]uint32{row, column}
	if from == to {
		return errRefused
	}
	path := op.m.findPath(from, to, op.state.Fleets, fleet)
	if path == nil {
		return errRefused
	}
	path = op.m.slide(path, op.state.Fleets, fleet)
	end := path[len(path)-1]
	if err := op.destroyInvisibleDeviceAt(from); err != nil {
		return err
	}
	op.mark(from)
	fleet.Row, fleet.Column = end[0], end[1]
	op.mark(end)
	for _, cell := range path {
		if err := op.destroyInvisibleDeviceAt(cell); err != nil {
			return err
		}
	}
	for _, cell := range path {
		op.reply.MovePath = append(op.reply.MovePath, worldCellPos(cell[0], cell[1]))
		discoverAround(op.m, cell[0], cell[1])
	}
	op.state.Count.Step += uint32(len(path))
	op.happened.Steps += uint32(len(path))
	op.state.Round++
	op.salvageStep()
	op.retakeMove()
	return nil
}

// salvageStep is a turn of movement for the meowfficer searches under way
// on the map: each finds its next node (progress, the client's
// UpdateCatSalvage; a search of another map stays as it is, the client
// ends it).
func (op *worldOp) salvageStep() {
	for _, fleet := range op.state.Fleets {
		salvage := &fleet.Salvage
		if salvage.RandomID == op.m.RandomID && salvage.Progress < uint32(len(salvage.List)) {
			salvage.Progress++
			if !slices.Contains(op.salvages, fleet.ID) {
				op.salvages = append(op.salvages, fleet.ID)
			}
		}
	}
}

// salvageSet is the world_catsearch_node set (1-4: ids 101-120...) a
// fleet's meowfficers search with: their best rarity (2 N to 5 SSR).
func (op *worldOp) salvageSet(fleet *worldFleet) (uint32, error) {
	best := uint32(2)
	for _, commander := range fleet.Commanders {
		cat, err := op.tx.GetMeowfficer(op.tx.ctx, db.GetMeowfficerParams{CommanderID: op.tx.commanderID, ID: commander.ID})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		best = max(best, meowfficerTemplates[cat.TemplateID].Rarity)
	}
	return min(max(best, 2), 5) - 1, nil
}

// round is OpReqRound: the turn after the player's (World:GetRound odd).
// The enemies don't move (the generated maps have no TypeEnemyAI); a fleet
// left on a wind is blown on by its strength (ai_act_list, a TypeFleet
// action: BuildFleetAction moves it along move_path).
func (op *worldOp) round() error {
	op.state.Round++
	template := worldTemplates[op.m.TemplateID]
	for _, fleet := range op.state.Fleets {
		if fleet.Submarine {
			continue
		}
		from := fleetCell(fleet)
		land, ok := op.m.land(from)
		if !ok || land.Type != worldTerrainWind {
			continue
		}
		var path [][2]uint32
		cell := from
		for range max(land.Distance, 1) {
			next := [2]uint32{uint32(int32(cell[0]) + land.DirRow), uint32(int32(cell[1]) + land.DirColumn)}
			if !template.isWalkable(next[0], next[1]) || !op.m.stayPoint(next, op.state.Fleets, fleet) {
				break
			}
			path = append(path, next)
			cell = next
		}
		if len(path) == 0 {
			continue
		}
		fleet.Row, fleet.Column = cell[0], cell[1]
		act := &protobuf.AI_ACT_P33{AiPos: worldCellPos(from[0], from[1]), Type: proto.Uint32(worldAttachFleet)}
		for _, step := range path {
			act.MovePath = append(act.MovePath, worldCellPos(step[0], step[1]))
		}
		act.PosList = []*protobuf.WORLDPOSINFO{op.m.posInfo(from[0], from[1], op.state.Fleets), op.m.posInfo(cell[0], cell[1], op.state.Fleets)}
		op.reply.AiActList = append(op.reply.AiActList, act)
		op.happened.Steps += uint32(len(path))
		op.state.Count.Step += uint32(len(path))
	}
	return nil
}

// discover is OpReqDiscover: the cells the client found in its fleets'
// sight (WorldMap:CheckDiscover, or the cells passed on a move).
func (op *worldOp) discover(cells []*protobuf.CHAPTERCELLPOS_P33) error {
	template := worldTemplates[op.m.TemplateID]
	for _, pos := range cells {
		if template.hasCell(pos.GetRow(), pos.GetColumn()) {
			op.m.discover([2]uint32{pos.GetRow(), pos.GetColumn()})
		}
	}
	return nil
}

// openBox is OpReqBox: the box under the fleet opens (flag 1): its
// box_data_template effect, a branch on the map's difficulty ending in a
// drop (EffectEventDrop).
func (op *worldOp) openBox() error {
	fleet, err := op.normalFleet()
	if err != nil {
		return err
	}
	cell := fleetCell(fleet)
	var box *worldAttachment
	for _, attachment := range op.m.cellAttachments(cell[0], cell[1]) {
		if attachment.Type == worldAttachBox && attachment.alive() {
			box = attachment
		}
	}
	if box == nil {
		return errRefused
	}
	box.Flag = 1
	op.mark(cell)
	op.happened.Boxes = append(op.happened.Boxes, box.ID)
	if config, ok := boxTemplates[box.ID]; ok {
		id := op.resolveBranch(config.EffectID, nil)
		if effect, ok := worldEffects[id]; ok && effect.EffectType == worldEffectDrop {
			op.dropAwards(effectParams(effect))
		}
	}
	return nil
}

// submarine is OpReqSub, the submarine support order: for its action
// points (World:CalcOrderCost, without the ships' skill discounts) the
// submarine fleet supports the map's battles until the map changes, its
// ships repaired (World:ResetSubmarine).
func (op *worldOp) submarine() error {
	var sub *worldFleet
	for _, fleet := range op.state.Fleets {
		if fleet.Submarine {
			sub = fleet
		}
	}
	if sub == nil || !op.orderAvailable(worldOrderSubmarine) {
		return errRefused
	}
	if err := op.order(worldReqSub, worldCDCost(worldOpSettings.submarineCost, op.state.lastCD(worldReqSub), op.now.Unix())); err != nil {
		return err
	}
	op.state.Submarine = true
	op.repair(sub)
	sub.Bullet = sub.BulletMax
	return nil
}

// The orders of a map's instruction_available (SVOrderPanel locks them).
const (
	worldOrderSubmarine   = 0
	worldOrderVision      = 1
	worldOrderMaintenance = 2
)

// orderAvailable tells whether the active map allows an order: the Hard
// Arbiters' zones have no maintenance (the wiki's "Regroup and Rest").
func (op *worldOp) orderAvailable(order int) bool {
	available := worldTemplates[op.m.TemplateID].InstructionAvailable
	return order >= len(available) || available[order] != 0
}

// repair is WorldMapShip:Repair for every ship of a fleet.
func (op *worldOp) repair(fleet *worldFleet) {
	for i := range fleet.Ships {
		ship := &fleet.Ships[i]
		ship.HPRant = fullHP
		ship.Buffs = slices.DeleteFunc(ship.Buffs, func(buff worldBuff) bool { return buff.ID == worldPortSettings.brokenBuff })
		op.markShip(ship.ID)
	}
}

// maintenance is OpReqMaintenance: every fleet repaired and its morale
// damage cleared, for world_instruction_maintenance's action points.
func (op *worldOp) maintenance() error {
	if !op.orderAvailable(worldOrderMaintenance) {
		return errRefused
	}
	if err := op.order(worldReqMaintenance, worldOpSettings.maintenanceCost[0]); err != nil {
		return err
	}
	for _, fleet := range op.state.Fleets {
		fleet.Damage = 0
		op.repair(fleet)
	}
	return nil
}

// vision is OpReqVision, the detection order: the map's attachments are
// shown (MAPINFO state flag 2) for its action points.
func (op *worldOp) vision() error {
	if !op.orderAvailable(worldOrderVision) {
		return errRefused
	}
	if err := op.order(worldReqVision, worldCDCost(worldOpSettings.visionCost, op.state.lastCD(worldReqVision), op.now.Unix())); err != nil {
		return err
	}
	op.m.Vision = true
	return nil
}

// pressing is OpReqPressingMap: the active map, left without an
// infection (refused while an event with an infection_value is on it), is
// secured once (clean_chapter; WorldMap:CheckMapPressing). The
// zone securing reward of its entrance, when there is one not taken
// (chapter_award flag 1, World:FlagMapPressingAward sets it to 0), is taken:
// its world_event_complete items (drop_list) and global buff (the client
// adds the buff itself in OpReqPressingMapDone).
func (op *worldOp) pressing(randomID uint32) error {
	if randomID != op.m.RandomID || worldRandomMaps[randomID].CompleteEffect == 0 || op.m.infection() > 0 {
		var infected []uint32
		for _, a := range op.m.Attachments {
			if a.Type == worldAttachEvent && worldEvents[a.ID].InfectionValue > 0 {
				infected = append(infected, a.ID)
			}
		}
		logger.WithFields("World",
			logger.FieldValue("map", randomID),
			logger.FieldValue("active", op.m.RandomID),
			logger.FieldValue("complete_effect", worldRandomMaps[randomID].CompleteEffect),
			logger.FieldValue("infection", op.m.infection()),
			logger.FieldValue("infected_events", infected),
		).Warn("pressing refused")
		return errRefused
	}
	if slices.Contains(op.state.CleanChapters, randomID) {
		return nil
	}
	op.state.CleanChapters = append(op.state.CleanChapters, randomID)
	op.happened.Pressed = append(op.happened.Pressed, randomID)
	op.securedEvents()
	for i := range op.state.ChapterAwards {
		award := &op.state.ChapterAwards[i]
		if worldColormasks[award.ID].Chapter != randomID || award.Flag != 1 {
			continue
		}
		award.Flag = 0
		reward := worldEventCompletes[award.Award]
		op.drops = append(op.drops, configDrops(reward.RewardItems)...)
		if len(reward.RewardBuff) == 2 {
			op.state.Buffs = addWorldBuff(op.state.Buffs, reward.RewardBuff[0], int64(reward.RewardBuff[1]))
		}
	}
	return nil
}

// securedEvents puts on a map just secured what its family has for it: an
// Explorable zone the Zone Control Reward (family + 300, which spawns its
// box, family + 301, around itself: a drop by difficulty, "Sector has
// been secured. Let us quickly collect the spoils of war."), a Safe Zone
// the event that flags it clear (family + 401, EffectEventMapClearFlag:
// the zone starts over once left).
func (op *worldOp) securedEvents() {
	offset := uint32(worldControllerSecured)
	if worldZones[op.m.RandomID] == worldZoneSafe {
		offset = worldControllerSafeClosed
	}
	id := worldFamilyEvent(op.m.TemplateID, offset)
	if id == 0 || slices.ContainsFunc(op.m.Attachments, func(a *worldAttachment) bool { return a.Type == worldAttachEvent && a.ID == id }) {
		return
	}
	for _, a := range spawnWorldEvent(op.m, worldTemplates[op.m.TemplateID], id, op.state.Fleets, op.rng) {
		op.mark([2]uint32{a.Row, a.Column})
	}
}

// assignPressingAwards hands out the zone securing rewards of an activation
// (chapter_award, World.pressingAwardDic, keyed by entrance:
// WSEntranceTpl / SVFloatPanel read GetPressingAward(entrance.id)): each
// world_event_complete row goes to chapter_num entrances, drawn among the
// ones whose base map can be secured (complete_effect) and isn't a port
// or a safe zone, flag 1 while not taken. Which entrances get what is the
// server's; the draw is seeded by the commander and the activation.
func assignPressingAwards(state *worldState, commanderID uint32) {
	if len(state.ChapterAwards) > 0 {
		return
	}
	var entrances []uint32
	for _, id := range slices.Sorted(maps.Keys(worldColormasks)) {
		mask := worldColormasks[id]
		random, ok := worldRandomMaps[mask.Chapter]
		if ok && mask.World == 1 && mask.PortMapIcon == 0 && random.CompleteEffect > 0 && random.HazardLevel > 0 {
			entrances = append(entrances, id)
		}
	}
	rng := worldRand(worldSeed(commanderID, state.Count.Activations, 0) ^ 0xa3a3)
	rng.Shuffle(len(entrances), func(i, j int) { entrances[i], entrances[j] = entrances[j], entrances[i] })
	for _, id := range slices.Sorted(maps.Keys(worldEventCompletes)) {
		for range worldEventCompleteCounts[id] {
			if len(entrances) == 0 {
				return
			}
			state.ChapterAwards = append(state.ChapterAwards, worldChapterAward{ID: entrances[0], Award: id, Flag: 1})
			entrances = entrances[1:]
		}
	}
}

// catSalvage is OpReqCatSalvage: a meowfficer search (EffectEventCatSalvage)
// ends, done (progress at the end of its list), called off or left
// behind (WorldMap:CheckFleetSalvage): the nodes reached give their loot
// (salvageLoot), the fleet's cmd_collection empties.
func (op *worldOp) catSalvage() error {
	if op.fleet == nil || op.fleet.Salvage.RandomID == 0 {
		return errRefused
	}
	salvage := op.fleet.Salvage
	op.drops = append(op.drops, salvageLoot(op.rng, salvage.List[:min(salvage.Progress, uint32(len(salvage.List)))])...)
	op.fleet.Salvage = worldSalvage{}
	op.salvages = append(op.salvages, op.fleet.ID)
	op.happened.Salvaged = append(op.happened.Salvaged, op.fleet.ID)
	return nil
}

// retreat is OpReqRetreat (the retreat button of a fleet on an enemy):
// the fleet backs off to the nearest cell nothing stands on
// (WorldMap:FindNearestBlankPoint), a TypeFleet AI action; when every
// way out is taken, to the nearest cell it can stop on that holds no
// enemy: the tutorial's Hornet (map 200, enemy 314001) is walled in by
// the 20003 events around it, which the guide's retreat lands the fleet
// on, and whose effects go on with the tutorial (the ring and Hornet
// removed, story GWORLD101D, progress 5, the teleport to map 201).
// Refused ("no_way_to_escape") when there is none.
func (op *worldOp) retreat() error {
	fleet, err := op.normalFleet()
	if err != nil {
		return err
	}
	from := fleetCell(fleet)
	if op.m.stageEnemy(from) == nil {
		return errRefused
	}
	path := op.m.blankPath(from, op.state.Fleets)
	if path == nil {
		path = op.m.stayPath(from, op.state.Fleets, fleet)
	}
	if path == nil {
		return errRefused
	}
	end := path[len(path)-1]
	fleet.Row, fleet.Column = end[0], end[1]
	act := &protobuf.AI_ACT_P33{AiPos: worldCellPos(from[0], from[1]), Type: proto.Uint32(worldAttachFleet)}
	for _, cell := range path {
		act.MovePath = append(act.MovePath, worldCellPos(cell[0], cell[1]))
	}
	act.PosList = []*protobuf.WORLDPOSINFO{op.m.posInfo(from[0], from[1], op.state.Fleets), op.m.posInfo(end[0], end[1], op.state.Fleets)}
	op.reply.AiActList = append(op.reply.AiActList, act)
	op.mark(from, end)
	return nil
}

// transport is OpReqTransport: the fleets sail to map randomID through
// entrance (an entrance the active one links to, map_transfer, or any
// other the atlas opens), arriving on the cell the client picked on the
// map's edge (WorldMap:CalcTransportPos). A map not in chapter_list yet
// costs its enter_cost (130 when short).
func (op *worldOp) transport(randomID, entrance uint32, cell [2]uint32, hasCell bool) error {
	mask, ok := worldColormasks[entrance]
	random, known := worldRandomMaps[randomID]
	if !ok || !known || slices.Contains(worldOpSettings.rookieMaps, op.m.RandomID) {
		return errRefused
	}
	if len(random.OpenStage) > 0 && random.OpenStage[0] > op.state.Count.Progress || mask.World == 0 {
		return errRefused
	}
	// a treasure map (teasure_chapter: an Obscured or Abyssal Zone) costs
	// its coordinates item, which the client takes off itself once in
	// (WorldMapOp:Apply, TreasureMap2ItemId then RemoveItem(item, 1)); it
	// only offers the map while one is held (EntranceToReplacementMapList).
	// Its templates all have is_clear, so the zone starts over once left
	// (needClear) and only another item opens it again.
	var coordinates uint32
	for _, treasure := range mask.TeasureChapter {
		if treasure[1] == randomID {
			coordinates = treasure[0]
			break
		}
	}
	if coordinates != 0 && op.state.item(coordinates) == 0 {
		return errRefused
	}
	// a Siren Stronghold only while its entrance shows it (sairen_chapter)
	if worldZones[randomID] == worldZoneStronghold && (!slices.Contains(op.state.SairenChapters, entrance) || !slices.Contains(mask.SairenChapter, randomID)) {
		return errRefused
	}
	var at *[2]uint32
	if hasCell {
		at = &cell
	}
	if err := op.switchMap(randomID, entrance, at, true); err != nil {
		return err
	}
	if coordinates != 0 {
		op.state.addItem(coordinates, -1)
	}
	return nil
}

// jumpOut is OpReqJumpOut: the fleets leave a map of
// world_chapter_template_reset for its transport_colormask entrance's base
// map, losing its reset_item world items.
func (op *worldOp) jumpOut() error {
	reset, ok := worldTemplateResets[op.m.TemplateID]
	if !ok {
		return errRefused
	}
	for _, item := range reset.ResetItem {
		op.state.addItem(item, -int64(op.state.item(item)))
	}
	entrance := reset.TransportColormask
	if _, ok := worldColormasks[entrance]; !ok {
		entrance = op.state.EnterMapID
	}
	return op.switchMap(worldColormasks[entrance].Chapter, entrance, nil, false)
}

// switchMap makes randomID the active map through entrance (the
// transfers: OpReqTransport, OpReqJumpOut, the teleport effects), what
// the client's NetUpdateActiveMap does: the round starts over, the
// submarine support ends (World:OnSwitchMap), the fleets go to cell (the
// active one; the others next to it) or the map's start cells, the map
// left behind starts over when it must (WorldMap:NeedClear). pay charges
// a map not in chapter_list yet its enter_cost.
func (op *worldOp) switchMap(randomID, entrance uint32, cell *[2]uint32, pay bool) error {
	random, ok := worldRandomMaps[randomID]
	if !ok {
		return errRefused
	}
	state := op.state
	if pay && state.mapTemplate(randomID) == 0 && !state.Stamina.consume(random.EnterCost) {
		return errWorldStamina
	}
	dest := op.m
	if randomID != op.m.RandomID {
		var err error
		if dest, err = op.tx.worldMap(state, randomID); err != nil {
			return err
		}
		if dest == nil || worldTemplates[dest.TemplateID] == nil {
			return errRefused
		}
		old := op.m
		op.left = append(op.left, old)
		if old.needClear() {
			op.cleared = append(op.cleared, old.RandomID)
			state.Maps = slices.DeleteFunc(state.Maps, func(m worldMapID) bool { return m.RandomID == old.RandomID })
			if worldZones[old.RandomID] == worldZoneStronghold {
				state.strongholdCleared(old.RandomID, op.tx.commanderID, op.now)
			}
		}
	}
	state.enterMap(dest)
	state.MapID = randomID
	state.EnterMapID = entrance
	state.Round = 0
	state.Submarine = false
	op.m = dest
	op.cells, op.lands = nil, nil
	op.placeFleets(cell)
	for _, fleet := range state.Fleets {
		if !fleet.Submarine {
			op.mark(fleetCell(fleet))
		}
	}
	op.reply.EnterMapId = proto.Uint32(entrance)
	op.reply.Id = dest.mapID()
	op.happened.Entered = append(op.happened.Entered, randomID)
	return nil
}

// infection is WorldMap:GetEventPoisonRate: the infection_value of the
// events on the map.
func (m *worldMap) infection() uint32 {
	total := uint32(0)
	for _, a := range m.Attachments {
		if a.Type == worldAttachEvent {
			total += worldEvents[a.ID].InfectionValue
		}
	}
	return total
}

// invisibleInfectionDevice is an event the client draws nothing for (no
// icon or name) that can only be triggered automatically (no event_op, no
// interactive effect) and still carries an infection: the intact Siren
// sea-condition controllers. Moving a fleet next to one destroys it.
func invisibleInfectionDevice(id uint32) bool {
	event, ok := worldEvents[id]
	if !ok || event.InfectionValue == 0 || event.Icon != "" || event.Name != "" || len(event.EventOp) > 0 {
		return false
	}
	for _, eid := range event.Effect {
		effect, ok := worldEffects[eid]
		if !ok || effect.AutoActivate != 0 {
			continue
		}
		switch effect.EffectType {
		case worldEffectTeleport, worldEffectBattle, worldEffectStoryOption, worldEffectCondition:
			return false
		}
	}
	return true
}

// destroyInvisibleDeviceAt runs the effects of the invisible infection
// device on cell (the client draws nothing for it and cannot attack it, so
// a fleet walking onto or through its cell is the trigger) and removes it:
// the zone's infection clears and its effects spawn the damaged device.
func (op *worldOp) destroyInvisibleDeviceAt(cell [2]uint32) error {
	for _, a := range slices.Clone(op.m.Attachments) {
		if a.Type != worldAttachEvent || !invisibleInfectionDevice(a.ID) || a.Row != cell[0] || a.Column != cell[1] {
			continue
		}
		ev := a
		ids := slices.Clone(ev.Effects)
		ev.Effects = nil
		for _, id := range ids {
			if err := op.runEffect(ev, id, 0); err != nil {
				return err
			}
		}
		op.m.removeAttachment(ev)
		op.mark([2]uint32{ev.Row, ev.Column})
		logger.WithFields("World",
			logger.FieldValue("map", op.m.RandomID),
			logger.FieldValue("event", ev.ID),
		).Info("destroyed invisible infection device")
	}
	return nil
}

// needClear is WorldMap:NeedClear: a map that starts over once left (a
// Siren map with no infection left, one flagged clear by an event, or a
// template that always does), and a Safe Zone cleared (the wiki:
// "Clearing a Secured zone and leaving will reset it"; its closing event
// flags it, but only on the visit it is secured on).
func (m *worldMap) needClear() bool {
	template := worldTemplates[m.TemplateID]
	return template != nil && template.IsSairen > 0 && m.infection() == 0 || m.Clear || template != nil && template.IsClear > 0 ||
		worldZones[m.RandomID] == worldZoneSafe && m.infection() == 0
}

// placeFleets puts the normal fleets on the active map: the acting fleet
// on cell when it can stop there, the others on the free cells nearest it;
// without a cell, on the map's start cells (worldStartCells).
func (op *worldOp) placeFleets(cell *[2]uint32) {
	var normal []*worldFleet
	for _, fleet := range op.state.Fleets {
		if !fleet.Submarine {
			fleet.Row, fleet.Column = ^uint32(0), ^uint32(0)
			normal = append(normal, fleet)
		}
	}
	template := worldTemplates[op.m.TemplateID]
	var cells [][2]uint32
	if cell != nil && template.isWalkable(cell[0], cell[1]) && op.m.stayPoint(*cell, nil, nil) {
		cells = append(cells, *cell)
		for _, free := range op.m.nearestFree(*cell, len(normal)-1) {
			cells = append(cells, free)
		}
	} else {
		cells = worldStartCells(op.m, len(normal))
	}
	if len(cells) == 0 {
		return
	}
	// the acting fleet first
	if op.fleet != nil && !op.fleet.Submarine {
		slices.SortStableFunc(normal, func(a, b *worldFleet) int {
			switch {
			case a == op.fleet:
				return -1
			case b == op.fleet:
				return 1
			}
			return 0
		})
	}
	for i, fleet := range normal {
		if i >= len(cells) {
			cells = append(cells, cells[len(cells)-1])
		}
		fleet.Row, fleet.Column = cells[i][0], cells[i][1]
		fleet.StartRow, fleet.StartCol = fleet.Row, fleet.Column
		discoverAround(op.m, fleet.Row, fleet.Column)
	}
}
