package game

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// A commander has at most one boss of their own, summoned with META
// Coordinates (the current season's META ship) or Dossier Coordinate Data
// (the Dossier Analysis target). Its id is the commander's, as the
// official bosses' ids are their owners'. This server has no other players,
// so there are never other bosses to join (the challenge attempts they cost,
// SC_34502.fight_count, stay unused) and a boss's ranking is its owner.

// WorldBoss.SUPPORT_TYPE_*: whom a support request goes to (CS_34509)
const (
	worldSupportFriend = 1
	worldSupportGuild  = 2
	worldSupportWorld  = 3
)

// worldBossNone is the result CheckWorldBossStateCommand and
// BattleGateWorldBoss show "Coordinates have expired" for (world_boss_none).
var worldBossNone error = refusal{"no such boss", 1}

// worldBoss is a commander's world_bosses row.
type worldBoss struct {
	db.WorldBoss
}

// loadWorldBoss reads the commander's META Showdown state as of now: the
// daily coordinate counters start again each day (WorldProxy's DayCall
// clears them), the current season's boss level when the season changed.
func loadWorldBoss(ctx context.Context, queries *db.Queries, commanderID uint32, now time.Time) (*worldBoss, error) {
	if err := queries.EnsureWorldBoss(ctx, commanderID); err != nil {
		return nil, err
	}
	row, err := queries.GetWorldBoss(ctx, commanderID)
	if err != nil {
		return nil, err
	}
	boss := &worldBoss{row}
	if day := startOfDay(now).Unix(); boss.AccDay != day {
		boss.AccDay = day
		boss.SummonPtDailyAcc = 0
		boss.SummonPtOldDailyAcc = 0
	}
	if current := currentWorldBoss(now); current != 0 && boss.SelfBossTemplateID != current {
		boss.SelfBossTemplateID = current
		boss.SelfBossLv = 1
	}
	return boss, nil
}

func (boss *worldBoss) save(ctx context.Context, queries *db.Queries) error {
	return queries.SaveWorldBoss(ctx, db.SaveWorldBossParams{
		SummonPt:            boss.SummonPt,
		SummonPtOld:         boss.SummonPtOld,
		SummonPtDailyAcc:    boss.SummonPtDailyAcc,
		SummonPtOldDailyAcc: boss.SummonPtOldDailyAcc,
		AccDay:              boss.AccDay,
		SummonFree:          boss.SummonFree,
		ArchivesID:          boss.ArchivesID,
		SelfBossLv:          boss.SelfBossLv,
		SelfBossTemplateID:  boss.SelfBossTemplateID,
		BossTemplateID:      boss.BossTemplateID,
		BossLv:              boss.BossLv,
		BossHp:              boss.BossHp,
		BossLastTime:        boss.BossLastTime,
		BossKillTime:        boss.BossKillTime,
		BossFightCount:      boss.BossFightCount,
		BossDamage:          boss.BossDamage,
		AutoFightFinishTime: boss.AutoFightFinishTime,
		AutoFightMaxDamage:  boss.AutoFightMaxDamage,
		GuildSupport:        boss.GuildSupport,
		FriendSupport:       boss.FriendSupport,
		WorldSupport:        boss.WorldSupport,
		CommanderID:         boss.CommanderID,
	})
}

// summoned is whether the commander has a boss (alive or not, expired or not).
func (boss *worldBoss) summoned() bool {
	return boss.BossTemplateID != 0
}

// fightable is whether the commander's boss can be fought at now: there, alive
// and not expired.
func (boss *worldBoss) fightable(now time.Time) bool {
	return boss.summoned() && boss.BossHp > 0 && now.Unix() < boss.BossLastTime
}

// clearSelfBoss removes the commander's boss and what came with it
// (WorldBossProxy:RemoveSelfBoss).
func (boss *worldBoss) clearSelfBoss() {
	boss.BossTemplateID, boss.BossLv, boss.BossHp = 0, 0, 0
	boss.BossLastTime, boss.BossKillTime = 0, 0
	boss.BossFightCount, boss.BossDamage = 0, 0
	boss.AutoFightFinishTime, boss.AutoFightMaxDamage = 0, 0
	boss.GuildSupport, boss.FriendSupport, boss.WorldSupport = 0, 0, 0
}

// damage takes amount off the commander's boss; a kill raises the level of
// the next boss of the season (up to worldBossMaxLevel): "after defeating a
// META boss, the difficulty of subsequent META Showdowns will increase by 1
// level" (gametip world_boss_help_meta, the CN text alike), where the wiki
// has every summon raise it.
func (boss *worldBoss) damage(amount uint32) {
	amount = min(amount, boss.BossHp)
	boss.BossHp -= amount
	boss.BossDamage += amount
	if boss.BossHp == 0 && amount > 0 && boss.BossTemplateID == boss.SelfBossTemplateID && !archivedWorldBoss(boss.BossTemplateID) {
		boss.SelfBossLv = min(boss.SelfBossLv+1, worldBossMaxLevel)
	}
}

// info is the commander's boss as WorldBoss:Setup reads it; rank_count
// counts its owner.
func (boss *worldBoss) info() *protobuf.WORLDBOSS_INFO_P34 {
	return &protobuf.WORLDBOSS_INFO_P34{
		Id:         proto.Uint32(boss.CommanderID),
		TemplateId: proto.Uint32(boss.BossTemplateID),
		Lv:         proto.Uint32(boss.BossLv),
		Hp:         proto.Uint32(boss.BossHp),
		Owner:      proto.Uint32(boss.CommanderID),
		LastTime:   proto.Uint32(uint32(boss.BossLastTime)),
		KillTime:   proto.Uint32(uint32(boss.BossKillTime)),
		FightCount: proto.Uint32(boss.BossFightCount),
		RankCount:  proto.Uint32(1),
	}
}

// addSummonPt adds coordinates (vitemWorldBossPt or vitemWorldBossPastPt),
// as WorldBossProxy:AddSummonPt: at most the ticket's daily gain, up to its
// capacity.
func (boss *worldBoss) addSummonPt(item, count uint32) {
	ticket, pt, acc := worldBossCurrent, &boss.SummonPt, &boss.SummonPtDailyAcc
	if item == vitemWorldBossPastPt {
		ticket, pt, acc = worldBossPast, &boss.SummonPtOld, &boss.SummonPtOldDailyAcc
	}
	count = min(count, ticket.dailyMax-min(*acc, ticket.dailyMax))
	gained := min(count, ticket.capacity-min(*pt, ticket.capacity))
	*pt += gained
	*acc = min(*acc+gained, ticket.dailyMax)
}

// addWorldBossSummonPt stores coordinates dropped in Operation Siren
// (grantDrops).
func (tx *Tx) addWorldBossSummonPt(item, count uint32) error {
	boss, err := loadWorldBoss(tx.ctx, tx.Queries, tx.commanderID, serverNow())
	if err != nil {
		return err
	}
	boss.addSummonPt(item, count)
	return boss.save(tx.ctx, tx.Queries)
}

// addWorldBossSummonFree counts a WorldMapAttachment
// EffectEventAddWorldBossFreeCount (WorldBossProxy:AddSummonFree).
func (tx *Tx) addWorldBossSummonFree(count uint32) error {
	boss, err := loadWorldBoss(tx.ctx, tx.Queries, tx.commanderID, serverNow())
	if err != nil {
		return err
	}
	boss.SummonFree += count
	return boss.save(tx.ctx, tx.Queries)
}

// updateWorldBoss loads the commander's boss state, runs fn on it and saves
// it, in tx.
func (tx *Tx) updateWorldBoss(now time.Time, fn func(boss *worldBoss) error) error {
	boss, err := loadWorldBoss(tx.ctx, tx.Queries, tx.commanderID, now)
	if err != nil {
		return err
	}
	if err := fn(boss); err != nil {
		return err
	}
	return boss.save(tx.ctx, tx.Queries)
}

// handleWorldBoss answers GetWorldBossCommand (CS_34501, after SC_33114 at
// login and on opening the META Showdown): the commander's boss and
// coordinates. pt (challenge attempts on others' bosses) is
// joint_boss_ap_max - fight_count; the client then sends CS_34513 for a boss
// left over from an ended season.
func (game *Game) handleWorldBoss(client *connection.Client, req *protobuf.CS_34501) error {
	ctx := context.Background()
	boss, err := loadWorldBoss(ctx, game.DB.Queries, client.CommanderID, serverNow())
	if err != nil {
		return err
	}
	reply := &protobuf.SC_34502{
		FightCount:           proto.Uint32(0),
		FightCountUpdateTime: proto.Uint32(0),
		SummonPt:             proto.Uint32(boss.SummonPt),
		SummonPtOld:          proto.Uint32(boss.SummonPtOld),
		SummonPtDailyAcc:     proto.Uint32(boss.SummonPtDailyAcc),
		SummonPtOldDailyAcc:  proto.Uint32(boss.SummonPtOldDailyAcc),
		SummonFree:           proto.Uint32(boss.SummonFree),
		AutoFightFinishTime:  proto.Uint32(uint32(boss.AutoFightFinishTime)),
		DefaultBossId:        proto.Uint32(boss.ArchivesID),
		AutoFightMaxDamage:   proto.Uint32(boss.AutoFightMaxDamage),
		GuildSupport:         proto.Uint32(uint32(boss.GuildSupport)),
		FriendSupport:        proto.Uint32(uint32(boss.FriendSupport)),
		WorldSupport:         proto.Uint32(uint32(boss.WorldSupport)),
		SelfBossLv:           proto.Uint32(boss.SelfBossLv),
	}
	if boss.summoned() {
		reply.SelfBoss = boss.info()
	}
	return client.Send(reply)
}

// handleActiveWorldBoss answers ActiveWorldBossCommand (CS_34521): summons
// the current season's boss at the commander's level for curr_boss_ticket's
// META Coordinates, or the Dossier Analysis target at level 15 for
// past_joint_boss_ticket's Dossier Coordinate Data (the client takes them
// itself on result 0). The boss has full hp for joint_boss_bonus_time
// seconds; refused while one is still up. Built from the Lua alone.
func (game *Game) handleActiveWorldBoss(client *connection.Client, req *protobuf.CS_34521) error {
	ctx := context.Background()
	now := serverNow()
	template := req.GetTemplateId()
	reply := &protobuf.SC_34522{Result: result(nil)}
	err := game.tx(ctx, client, func(tx *Tx) error {
		return tx.updateWorldBoss(now, func(boss *worldBoss) error {
			if boss.summoned() && now.Unix() < boss.BossLastTime {
				logger.WithFields("WorldBoss",
					logger.FieldValue("template", template),
					logger.FieldValue("reason", "boss still up"),
					logger.FieldValue("boss_last_time", boss.BossLastTime),
				).Warn("active world boss refused")
				return errRefused
			}
			level := boss.SelfBossLv
			current := currentWorldBoss(now)
			switch {
			case template != 0 && template == current:
				if boss.SummonPt < worldBossCurrent.consume {
					logger.WithFields("WorldBoss",
						logger.FieldValue("template", template),
						logger.FieldValue("reason", "not enough META coordinates"),
						logger.FieldValue("summon_pt", boss.SummonPt),
						logger.FieldValue("consume", worldBossCurrent.consume),
					).Warn("active world boss refused")
					return ErrNotEnough
				}
				boss.SummonPt -= worldBossCurrent.consume
			case archivedWorldBoss(template) && template == boss.ArchivesID:
				if boss.SummonPtOld < worldBossPast.consume {
					logger.WithFields("WorldBoss",
						logger.FieldValue("template", template),
						logger.FieldValue("reason", "not enough Dossier Coordinate Data"),
						logger.FieldValue("summon_pt_old", boss.SummonPtOld),
						logger.FieldValue("consume", worldBossPast.consume),
					).Warn("active world boss refused")
					return ErrNotEnough
				}
				boss.SummonPtOld -= worldBossPast.consume
				level = worldBossMaxLevel
			default:
				logger.WithFields("WorldBoss",
					logger.FieldValue("template", template),
					logger.FieldValue("current", current),
					logger.FieldValue("archives_id", boss.ArchivesID),
					logger.FieldValue("archived", archivedWorldBoss(template)),
				).Warn("active world boss refused")
				return errRefused
			}
			boss.clearSelfBoss()
			boss.BossTemplateID = template
			boss.BossLv = level
			boss.BossHp = worldBossLevel(template, level).HP
			boss.BossLastTime = now.Unix() + int64(worldBossLifetime)
			if err := tx.ClearWorldBossShips(ctx, client.CommanderID); err != nil {
				return err
			}
			reply.Boss = boss.info()
			return nil
		})
	})
	if refused(err) {
		return client.Send(&protobuf.SC_34522{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}

// handleWorldBossRank answers GetWBDamageRankCommand (CS_34505): the damage
// dealt to a boss by each participant, here its owner alone.
func (game *Game) handleWorldBossRank(client *connection.Client, req *protobuf.CS_34505) error {
	ctx := context.Background()
	boss, err := loadWorldBoss(ctx, game.DB.Queries, client.CommanderID, serverNow())
	if err != nil {
		return err
	}
	reply := &protobuf.SC_34506{}
	if !boss.summoned() || req.GetBossId() != client.CommanderID {
		return client.Send(reply)
	}
	commander, err := game.DB.GetCommander(ctx, client.CommanderID)
	if err != nil {
		return err
	}
	reply.RankList = append(reply.RankList, &protobuf.WORLDBOSS_RANK_P34{
		Id:     proto.Uint32(commander.ID),
		Name:   proto.String(commander.Name),
		Damage: proto.Uint32(boss.BossDamage),
	})
	return client.Send(reply)
}

// handleWorldBossSupport answers WorldBossSupportCommand (CS_34509): asks
// friends, the guild or everyone for help with the commander's boss for
// joint_boss_world_time seconds (the client starts the same cooldown on
// result 0). The request is announced to those it goes to with SC_34507;
// with nobody else on this server, only to the commander, whose boss it
// updates. Built from the Lua alone.
func (game *Game) handleWorldBossSupport(client *connection.Client, req *protobuf.CS_34509) error {
	ctx := context.Background()
	now := serverNow()
	var info *protobuf.WORLDBOSS_INFO_P34
	err := game.tx(ctx, client, func(tx *Tx) error {
		return tx.updateWorldBoss(now, func(boss *worldBoss) error {
			if !boss.fightable(now) {
				return errRefused
			}
			until := now.Unix() + int64(worldBossSupportTime)
			switch req.GetType() {
			case worldSupportFriend:
				boss.FriendSupport = until
			case worldSupportGuild:
				boss.GuildSupport = until
			case worldSupportWorld:
				boss.WorldSupport = until
			default:
				return errRefused
			}
			info = boss.info()
			return nil
		})
	})
	if refused(err) {
		return client.Send(&protobuf.SC_34510{Result: result(err)})
	}
	if err != nil {
		return err
	}
	commander, err := game.DB.GetCommander(ctx, client.CommanderID)
	if err != nil {
		return err
	}
	if err := client.Send(&protobuf.SC_34510{Result: result(nil)}); err != nil {
		return err
	}
	return client.PushAfterReply(&protobuf.SC_34507{
		BossInfo: info,
		UserInfo: &protobuf.USERSIMPLEINFO{
			Id:      proto.Uint32(commander.ID),
			Name:    proto.String(commander.Name),
			Lv:      proto.Uint32(commander.Level),
			Display: game.displayInfo(commander),
		},
		Type: proto.Uint32(req.GetType()),
	})
}

// handleWorldBossAward answers SubmitWBAwardCommand (CS_34511): the reward
// of the commander's killed boss, the Sync Points of its META ship
// (worldBossKillPt), added by the client through PlayerConst.addTranDrop;
// the boss is gone afterwards (expired or not). Built from the Lua alone.
func (game *Game) handleWorldBossAward(client *connection.Client, req *protobuf.CS_34511) error {
	ctx := context.Background()
	reply := &protobuf.SC_34512{Result: result(nil)}
	err := game.tx(ctx, client, func(tx *Tx) error {
		return tx.updateWorldBoss(serverNow(), func(boss *worldBoss) error {
			if req.GetBossId() != client.CommanderID || !boss.summoned() || boss.BossHp > 0 {
				return errRefused
			}
			meta := worldBossTemplates[boss.BossTemplateID].MetaID
			reply.Drops = []*protobuf.DROPINFO{dropInfo(dropTypeMetaPt, meta, worldBossKillPt(boss.BossLv))}
			boss.clearSelfBoss()
			return tx.grantDrops(reply.Drops)
		})
	})
	if refused(err) {
		return client.Send(&protobuf.SC_34512{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}

// handleWorldBossOvertime answers the CS_34513 of WorldBossOverTimeCommand
// and GetWorldBossCommand: the commander's boss, alive, expired or left
// from an ended season, is given up (a killed one is claimed with CS_34511
// instead). The client ignores the result.
func (game *Game) handleWorldBossOvertime(client *connection.Client, req *protobuf.CS_34513) error {
	ctx := context.Background()
	now := serverNow()
	err := game.tx(ctx, client, func(tx *Tx) error {
		return tx.updateWorldBoss(now, func(boss *worldBoss) error {
			if !boss.summoned() || boss.BossHp == 0 {
				return nil
			}
			ended := !archivedWorldBoss(boss.BossTemplateID) && boss.BossTemplateID != currentWorldBoss(now)
			if now.Unix() >= boss.BossLastTime || ended {
				boss.clearSelfBoss()
			}
			return nil
		})
	})
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_34514{Result: result(nil)})
}

// handleCheckWorldBoss answers CheckWorldBossStateCommand (CS_34515, before
// the formation screen of a boss): 0 when it can be fought, 1 (the
// "Coordinates have expired" tip, the boss dropped from the list) otherwise.
func (game *Game) handleCheckWorldBoss(client *connection.Client, req *protobuf.CS_34515) error {
	ctx := context.Background()
	now := serverNow()
	boss, err := loadWorldBoss(ctx, game.DB.Queries, client.CommanderID, now)
	if err != nil {
		return err
	}
	if req.GetBossId() != client.CommanderID || !boss.fightable(now) {
		return client.Send(&protobuf.SC_34516{Result: result(worldBossNone)})
	}
	return client.Send(&protobuf.SC_34516{Result: result(nil)})
}

// handleCacheBossHP answers GetCacheBossHpCommand (CS_34517): the hp and
// participants of the other players' bosses the client lists. There are
// none on this server; the commander's own boss is answered if asked.
func (game *Game) handleCacheBossHP(client *connection.Client, req *protobuf.CS_34517) error {
	ctx := context.Background()
	boss, err := loadWorldBoss(ctx, game.DB.Queries, client.CommanderID, serverNow())
	if err != nil {
		return err
	}
	reply := &protobuf.SC_34518{}
	for _, id := range req.GetBossId() {
		if id == client.CommanderID && boss.summoned() {
			reply.List = append(reply.List, &protobuf.WORLDBOSS_SIMPLE{
				Id:        proto.Uint32(id),
				Hp:        proto.Uint32(boss.BossHp),
				RankCount: proto.Uint32(1),
			})
		}
	}
	return client.Send(reply)
}

// handleWorldBossFormation answers WorldBossGetOtherFormationCommand
// (CS_34519, a row of the damage ranking): the ships the participant last
// fought the boss with, here the commander's (MetaBossRankShip, full
// SHIPINFO).
func (game *Game) handleWorldBossFormation(client *connection.Client, req *protobuf.CS_34519) error {
	ctx := context.Background()
	if req.GetBossId() != client.CommanderID || req.GetUserId() != client.CommanderID {
		return client.Send(&protobuf.SC_34520{Result: result(errRefused)})
	}
	ids, err := game.DB.ListWorldBossShips(ctx, client.CommanderID)
	if err != nil {
		return err
	}
	ships, err := guildAssaultShipInfos(ctx, game.DB.Queries, client.CommanderID, ids)
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_34520{Result: result(nil), ShipList: ships})
}

// handleSwitchWorldBossArchives answers SwitchWorldBossArchivesCommand
// (CS_34527): makes a Dossier Analysis boss the one Dossier Coordinate Data
// summons (default_boss_id). The boss already up stays.
func (game *Game) handleSwitchWorldBossArchives(client *connection.Client, req *protobuf.CS_34527) error {
	ctx := context.Background()
	err := game.tx(ctx, client, func(tx *Tx) error {
		return tx.updateWorldBoss(serverNow(), func(boss *worldBoss) error {
			if !archivedWorldBoss(req.GetBossId()) {
				return errRefused
			}
			boss.ArchivesID = req.GetBossId()
			return nil
		})
	})
	if refused(err) {
		return client.Send(&protobuf.SC_34528{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(&protobuf.SC_34528{Result: result(nil)})
}
