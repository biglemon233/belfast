package game

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

const (
	// maxBuilds is the client's MAX_BUILD_WORK_COUNT: builds that can be
	// queued at once
	maxBuilds = 10
	// buildWorkCount is how many of them progress at the same time
	// (SC_12024.worklist_count as captured), the next one starts when one
	// finishes (BuildShipProxy:activeNextBuild)
	buildWorkCount = 2
)

// The new player guide's builds (gamecfg/guide/newguide/sg001.lua: S001 light,
// S020 heavy) are ordinary draws: the guide only needs a build in the pool it
// picks client side, either still building (S002, quick finish) or done
// (S002_1, getFinishCount), whose ship it then puts in the vanguard (S004),
// which every light pool ship can join (destroyers, light cruisers, vanguard
// sailing ships). The official server's draw gave Hatsuharu (301211).

// rarityByRateName maps the English rate labels of the display texts (tech
// packs, commander_build_rate_tip) to ship_data_statistics rarity: those of
// gametip index_rare2..5, but "Ultra Rare" where index_rare6 says "Ultra",
// so they are matched here rather than read from gametip. The other regions
// label the cat box rates R, SR and SSR.
var rarityByRateName = map[string]uint32{
	"Common": 2, "Rare": 3, "Elite": 4, "Super Rare": 5, "Ultra Rare": 6,
	"R": 3, "SR": 4, "SSR": 5,
}

// buildTimes are the construction times of the ships (base template id ->
// seconds), server-side data the client never sees and no table has:
// server/build_times.json of the data dir ({"<template id>": seconds}),
// scraped from the wiki by internal/tools/wiki_build_times.py (Hatsuharu's
// 1500 s matches the official capture). A ship it doesn't list is never
// drawn (loadShipData leaves it out of the pools); without the file no build
// can be started.
var buildTimes map[uint32]uint32

func loadBuildTimes() error {
	buildTimes = map[uint32]uint32{}
	raw, err := gamedata.ServerFile("build_times.json")
	if raw == nil || err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &buildTimes); err != nil {
		return fmt.Errorf("build times: %w", err)
	}
	return nil
}

// regularBuildExchangeID is REGULAR_BUILD_POOL_EXCHANGE_ID (const.lua): the
// ship_data_create_exchange row of the UR exchange of the permanent pools.
const regularBuildExchangeID = 1

// regularBuildExchange is that row: the build points an exchange costs,
// which is also as many as the client keeps
// (BuildShipProxy:changeRegularExchangeCount clamps to it), and the ships
// it offers.
var regularBuildExchange buildExchange

type buildExchange struct {
	Request uint32   `json:"exchange_request"`
	Ships   []uint32 `json:"exchange_ship_id"`
}

func loadBuildExchange() error {
	rows, err := gamedata.Table[buildExchange]("ship_data_create_exchange")
	if err != nil {
		return err
	}
	row, ok := rows[regularBuildExchangeID]
	if !ok || row.Request == 0 {
		return gamedata.Missing("ship_data_create_exchange", "row %d", regularBuildExchangeID)
	}
	regularBuildExchange = row
	return nil
}

// buildPoints are the commander's build points, at most the exchange's
// request (a database from before the cap may hold more).
func buildPoints(ctx context.Context, queries *db.Queries, commanderID uint32) (uint32, error) {
	points, err := queries.GetBuildPoints(ctx, commanderID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return min(points, regularBuildExchange.Request), nil
}

// buildDocksState is SC_12024 from storage: the builds in dock order (their
// index is the pos the client sends back) and the build points. draw_count_*
// count gem draws (draw_data_template), never done here.
func buildDocksState(ctx context.Context, queries *db.Queries, commanderID uint32) (*protobuf.SC_12024, error) {
	builds, err := queries.ListBuilds(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	points, err := buildPoints(ctx, queries, commanderID)
	if err != nil {
		return nil, err
	}
	msg := &protobuf.SC_12024{WorklistCount: proto.Uint32(buildWorkCount), ExchangeCount: proto.Uint32(points)}
	for _, build := range builds {
		msg.WorklistList = append(msg.WorklistList, buildInfo(build))
	}
	return msg, nil
}

// buildStartTime is when a build started, or starts while it is queued.
func buildStartTime(build db.Build) int64 {
	return build.FinishTime - int64(build.Duration)
}

// buildInfo is a BUILDINFO, read into a BuildShip (model/vo/buildship.lua).
func buildInfo(build db.Build) *protobuf.BUILDINFO {
	return &protobuf.BUILDINFO{
		Time:       proto.Uint32(build.Duration),
		FinishTime: proto.Uint32(uint32(build.FinishTime)),
		BuildId:    proto.Uint32(build.PoolID),
	}
}

// pickBuildShip decides what a build yields: a rarity drawn with the pool's
// rate_tip, then one of the pool's ships of that rarity (ship_data_group
// descriptions linking to the pool), built in its buildTimes time.
func pickBuildShip(pool buildPool) (template uint32, duration uint32, ok bool) {
	if len(pool.rates) == 0 {
		return 0, 0, false
	}
	roll := rand.Float64() * pool.total
	rarity := pool.rates[len(pool.rates)-1].rarity // if rounding leaves the roll positive
	for _, r := range pool.rates {
		if roll -= r.rate; roll < 0 {
			rarity = r.rarity
			break
		}
	}
	ships := pool.candidates[rarity]
	template = ships[rand.IntN(len(ships))]
	return template, buildTimes[template], true
}

// buildStart is when a new build behind placed ones starts: when one of the
// buildWorkCount docks frees up (builds are started in order, so those are
// the latest finish times), now at the earliest.
func buildStart(placed []db.Build, now int64) int64 {
	finishes := make([]int64, 0, len(placed))
	for _, build := range placed {
		finishes = append(finishes, build.FinishTime)
	}
	slices.Sort(finishes)
	slices.Reverse(finishes)
	if len(finishes) < buildWorkCount {
		return now
	}
	return max(now, finishes[buildWorkCount-1])
}

// handleBuildShip starts constructions (BuildShipCommand, CS_12002): count
// builds in pool id (ship_data_create_material), paid with use_gold gold and
// number_1 use_item each; costtype 1 pays with an event build ticket, there
// is none. Each build adds exchange_count build points (1 per Wisdom Cube in
// the permanent pools, none in those with a limited UR), up to the UR
// exchange's request as the client clamps them. The client takes the costs
// off its own state on result 0.
func (game *Game) handleBuildShip(client *connection.Client, req *protobuf.CS_12002) error {
	ctx := context.Background()
	now := time.Now().Unix()
	pool, ok := buildPools[req.GetId()]
	count := req.GetCount()
	var started []db.Build
	err := errRefused
	if ok && req.GetCosttype() == 0 && count >= 1 && count <= maxBuilds {
		err = game.tx(ctx, client, func(tx *Tx) error {
			builds, err := tx.ListBuilds(ctx, client.CommanderID)
			if err != nil {
				return err
			}
			if len(builds)+int(count) > maxBuilds {
				return errRefused
			}
			if err := tx.SpendResource(ResourceGold, pool.Gold*count); err != nil {
				return err
			}
			if err := tx.SpendItem(pool.Item, pool.ItemCount*count); err != nil {
				return err
			}
			for range count {
				template, duration, ok := pickBuildShip(pool)
				if !ok {
					return errRefused
				}
				start := buildStart(builds, now)
				build, err := tx.CreateBuild(ctx, db.CreateBuildParams{
					CommanderID: client.CommanderID,
					PoolID:      req.GetId(),
					TemplateID:  template,
					Duration:    duration,
					FinishTime:  start + int64(duration),
				})
				if err != nil {
					return err
				}
				builds = append(builds, build)
				started = append(started, build)
			}
			// capture: the task progress (SC_20102, SC_20202) comes before SC_12003
			tx.event(TaskEvent{SubType: TaskSubBuild, Count: count})
			return tx.addBuildPoints(int64(pool.ExchangeCount) * int64(count))
		})
	}
	if err != nil && !refused(err) {
		return err
	}
	reply := &protobuf.SC_12003{Result: result(err)}
	if err == nil {
		for _, build := range started {
			reply.BuildInfo = append(reply.BuildInfo, buildInfo(build))
		}
	}
	return client.Send(reply)
}

// addBuildPoints changes the commander's build points by delta, kept
// between 0 and the UR exchange's request as the client does
// (BuildShipProxy:changeRegularExchangeCount).
func (tx *Tx) addBuildPoints(delta int64) error {
	points, err := buildPoints(tx.ctx, tx.Queries, tx.commanderID)
	if err != nil {
		return err
	}
	points = uint32(min(max(int64(points)+delta, 0), int64(regularBuildExchange.Request)))
	return tx.SetBuildPoints(tx.ctx, db.SetBuildPointsParams{CommanderID: tx.commanderID, ExchangeCount: points})
}

// handleBuildExchange trades the build points for a ship of the permanent
// pools' UR exchange (BuildPoolRegularExchangeCommand, CS_12047 {ship_tid},
// from BuildShipRegularExchangeLayer): the client sends it once its points
// reach ship_data_create_exchange exchange_request, with one of that row's
// exchange_ship_id, and on result 0 takes the request off its points and
// shows drop_list (PlayerConst.addTranDrop); the ship itself reaches the dock
// in SC_12042, as other dropped ships. The client checks nothing else (no
// dock space). No official capture has one: the reply is built from the Lua
// alone.
func (game *Game) handleBuildExchange(client *connection.Client, req *protobuf.CS_12047) error {
	ctx := context.Background()
	drops := []*protobuf.DROPINFO{dropInfo(dropTypeShip, req.GetShipTid(), 1)}
	err := errRefused
	if slices.Contains(regularBuildExchange.Ships, req.GetShipTid()) {
		err = game.tx(ctx, client, func(tx *Tx) error {
			points, err := buildPoints(ctx, tx.Queries, client.CommanderID)
			if err != nil {
				return err
			}
			if points < regularBuildExchange.Request {
				return errRefused
			}
			if err := tx.addBuildPoints(-int64(regularBuildExchange.Request)); err != nil {
				return err
			}
			return tx.grantDrops(drops)
		})
	}
	if err != nil && !refused(err) {
		return err
	}
	if err != nil {
		drops = nil
	}
	return client.Send(&protobuf.SC_12048{Result: result(err), DropList: drops})
}

// buildsAt maps the dock positions the client sends (1-based, in build
// order) to builds, dropping duplicates and positions with no build.
func buildsAt(builds []db.Build, positions []uint32) (picked []db.Build, picks []uint32) {
	seen := map[uint32]bool{}
	for _, pos := range positions {
		if pos < 1 || int(pos) > len(builds) || seen[pos] {
			continue
		}
		seen[pos] = true
		picked = append(picked, builds[pos-1])
		picks = append(picks, pos)
	}
	return picked, picks
}

// handleBuildQuickFinish finishes builds now with a Quick Finisher each
// (BuildShipImmediatelyCommand, CS_12008); queued builds behind them start
// right away. Replies the positions finished.
func (game *Game) handleBuildQuickFinish(client *connection.Client, req *protobuf.CS_12008) error {
	ctx := context.Background()
	now := time.Now().Unix()
	var finished []uint32
	// capture: the task progress of the Quick Finishers used (SC_20202) comes
	// before SC_12009
	err := game.tx(ctx, client, func(tx *Tx) error {
		builds, err := tx.ListBuilds(ctx, client.CommanderID)
		if err != nil {
			return err
		}
		picked, positions := buildsAt(builds, req.GetPosList())
		for i, build := range picked {
			if build.FinishTime <= now {
				continue
			}
			if err := tx.SpendItem(ItemQuickFinisher, 1); err != nil {
				return err
			}
			builds[positions[i]-1].FinishTime = now
			finished = append(finished, positions[i])
		}
		if len(finished) == 0 {
			return errRefused
		}
		return rescheduleBuilds(ctx, tx.Queries, builds, now)
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_12009{Result: result(err), PosList: finished})
}

// rescheduleBuilds saves builds after some finished early: the ones not
// started yet move up behind the others.
func rescheduleBuilds(ctx context.Context, queries *db.Queries, builds []db.Build, now int64) error {
	var placed []db.Build
	for _, build := range builds {
		if buildStartTime(build) > now {
			build.FinishTime = buildStart(placed, now) + int64(build.Duration)
		}
		placed = append(placed, build)
		if err := queries.SetBuildFinishTime(ctx, db.SetBuildFinishTimeParams{FinishTime: build.FinishTime, ID: build.ID}); err != nil {
			return err
		}
	}
	return nil
}

// handleBuildResults tells which ship each finished build yields
// (GetShipCommand, CS_12043), so the client downloads their paintings before
// collecting them.
func (game *Game) handleBuildResults(client *connection.Client, req *protobuf.CS_12043) error {
	builds, err := game.DB.ListBuilds(context.Background(), client.CommanderID)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	reply := &protobuf.SC_12044{}
	for i, build := range builds {
		if build.FinishTime <= now {
			reply.InfoList = append(reply.InfoList, &protobuf.BUILD_INFO{Pos: proto.Uint32(uint32(i + 1)), Tid: proto.Uint32(build.TemplateID)})
		}
	}
	return client.Send(reply)
}

// handleCollectBuilds turns finished builds into ships (GetShipCommand,
// CS_12025). A ship of a new group first pushes the archive medal (SC_17002).
// A build of a META ship the commander already has still names that ship: the
// client's launch screen turns it into the broken crystals a duplicate gives,
// so the slot clears as usual, and the crystals are stored on this side too
// (the ship itself never is).
func (game *Game) handleCollectBuilds(client *connection.Client, req *protobuf.CS_12025) error {
	ctx := context.Background()
	now := time.Now().Unix()
	var ships []*protobuf.SHIPINFO
	err := game.tx(ctx, client, func(tx *Tx) error {
		builds, err := tx.ListBuilds(ctx, client.CommanderID)
		if err != nil {
			return err
		}
		picked, _ := buildsAt(builds, req.GetPosList())
		for _, build := range picked {
			if build.FinishTime > now {
				continue
			}
			if broken := metaBrokenCrystals(build.TemplateID); broken != 0 {
				owned, err := tx.ownsMetaShipGroup(build.TemplateID)
				if err != nil {
					return err
				}
				if owned {
					if err := tx.AddItem(itemMetaBrokenCrystal, broken); err != nil {
						return err
					}
					// the ship is the screen the client converts; it is not
					// the commander's afterwards
					ship, err := tx.AddShip(build.TemplateID)
					if err != nil {
						return err
					}
					if err := tx.DeleteShip(ctx, db.DeleteShipParams{ID: ship.GetId(), CommanderID: client.CommanderID}); err != nil {
						return err
					}
					if err := tx.DeleteBuild(ctx, build.ID); err != nil {
						return err
					}
					ships = append(ships, ship)
					continue
				}
			}
			ship, err := tx.AddShip(build.TemplateID)
			if err != nil {
				return err
			}
			if err := tx.DeleteBuild(ctx, build.ID); err != nil {
				return err
			}
			ships = append(ships, ship)
		}
		if len(ships) == 0 {
			return errRefused
		}
		return nil
	})
	if err != nil && !refused(err) {
		return err
	}
	if err := client.Send(&protobuf.SC_12026{Result: result(err), ShipList: ships}); err != nil {
		return err
	}
	return game.announceBuilds(ctx, client, ships)
}

const (
	// adConstruct is gametip ad_1, "<player> constructs <ship>", the ad_id
	// of the SC_50103 announcing a build
	adConstruct = 1
	// PublicArg.TypePlayerName, TypeShipId (model/vo/publicarg.lua)
	publicArgPlayerName = 1
	publicArgShipID     = 2
	// ShipRarity.Gray, Common
	shipRarityGray = 2
	// ShipRarity.Gold, Super Rare
	shipRarityGold = 5
)

// announceBuilds tells the chat room of the commander that collected builds
// about the Super Rare and Ultra Rare ships among them (SC_50103, ad_1: a
// PublicArg player name then ship id), which ChatProxy adds to the public
// channel. As captured: every one of the 39 announcements of the official
// captures is a rarity 5 or 6 ship (ShipRarity.Gold, .SSR) and the
// commander's own Rare build was not announced; ad_1 says "constructs".
// When the official server sends it is not in the captures (only other
// players' builds were announced): here on collection (CS_12025), when the
// ship joins the dock, not when the build timer runs out. Who gets it is
// not either: as world chat (handleWorldChat), the commanders online in the
// builder's chat room, the builder included.
func (game *Game) announceBuilds(ctx context.Context, client *connection.Client, ships []*protobuf.SHIPINFO) error {
	var announced []uint32
	for _, ship := range ships {
		if shipTemplates[ship.GetTemplateId()].Rarity >= shipRarityGold {
			announced = append(announced, ship.GetTemplateId())
		}
	}
	if len(announced) == 0 {
		return nil
	}
	builder, err := game.DB.GetCommander(ctx, client.CommanderID)
	if err != nil {
		return err
	}
	var msgs []proto.Message
	for _, template := range announced {
		msgs = append(msgs, &protobuf.SC_50103{
			AdId: proto.Uint32(adConstruct),
			ArgList: []*protobuf.AD_ARG{
				{Type: proto.Uint32(publicArgPlayerName), String_: proto.String(builder.Name)},
				{Type: proto.Uint32(publicArgShipID), Int: proto.Uint32(template)},
			},
		})
	}
	for _, id := range game.onlineCommanders() {
		reader, err := game.DB.GetCommander(ctx, id)
		if err != nil {
			return err
		}
		if reader.ChatRoomID == builder.ChatRoomID {
			game.deliver(client, id, msgs...)
		}
	}
	return nil
}

// handleConfirmBuilds acknowledges the "ships collected while away"
// notification (GetShipConfirmCommand, CS_12045, SC_11003.taking_ship_list);
// builds are only ever collected by the player, so there is nothing to clear.
func (game *Game) handleConfirmBuilds(client *connection.Client, req *protobuf.CS_12045) error {
	return client.Send(&protobuf.SC_12046{Result: result(nil)})
}
