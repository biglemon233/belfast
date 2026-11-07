package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/ggmolly/belfast/internal/game"
)

// registerProgress registers the API of a commander's timers, progress and research.
func (server *Server) registerProgress(api apiFunc) {
	api("GET /api/commanders/{id}/timers", server.handleTimers)
	api("POST /api/commanders/{id}/timers/finish", server.handleFinishTimer)
	api("POST /api/commanders/{id}/timers/finish-all", server.handleFinishAllTimers)
	api("POST /api/commanders/{id}/timers/reset", server.handleResetCounter)
	api("POST /api/commanders/{id}/build-points", server.handleBuildPoints)
	api("POST /api/commanders/{id}/fields/level", server.handleFieldLevel)

	api("GET /api/commanders/{id}/progress", server.handleProgress)
	api("POST /api/commanders/{id}/progress/stage", server.handleSetStage)
	api("POST /api/commanders/{id}/progress/unlock", server.handleUnlockChapters)
	api("POST /api/commanders/{id}/progress/end-sortie", server.handleEndSortie)
	api("POST /api/commanders/{id}/progress/mission", server.handleMissionProgress)
	api("POST /api/commanders/{id}/progress/guide", server.handleGuide)

	api("GET /api/commanders/{id}/research", server.handleResearch)
	api("POST /api/commanders/{id}/research/blueprint", server.handleDevelopBlueprint)
	api("POST /api/commanders/{id}/research/blueprint/unlock", server.handleUnlockBlueprint)
	api("POST /api/commanders/{id}/research/fleet-tech", server.handleFleetTechLevel)
	api("POST /api/commanders/{id}/research/fleet-tech/reset", server.handleFleetTechReset)
}

type progTimerShip struct {
	ID     uint32 `json:"id"`
	Name   string `json:"name"`
	Rarity uint32 `json:"rarity"`
	Icon   string `json:"icon,omitempty"`
}

type progBuildTimer struct {
	ID     uint32        `json:"id"`
	Ship   progTimerShip `json:"ship"`
	Pool   string        `json:"pool"`
	Start  int64         `json:"start"`
	Finish int64         `json:"finish"`
}

type progCommissionTimer struct {
	ID     uint32          `json:"id"`
	Name   string          `json:"name"`
	Start  int64           `json:"start"`
	Finish int64           `json:"finish"`
	Ships  []progTimerShip `json:"ships"`
}

type progLessonTimer struct {
	Room   uint32        `json:"room"`
	Ship   progTimerShip `json:"ship"`
	Skill  string        `json:"skill"`
	Book   string        `json:"book"`
	Start  int64         `json:"start"`
	Finish int64         `json:"finish"`
}

type progFieldTimer struct {
	Key       uint32 `json:"key"`
	Level     uint32 `json:"level"`
	MaxLevel  uint32 `json:"max_level"`
	UpgradeAt int64  `json:"upgrade_at"`
	// how long the running upgrade takes
	UpgradeTime int64 `json:"upgrade_time"`
	// what it holds, waiting to be collected
	Stored uint32 `json:"stored"`
}

type progAutoTimer struct {
	Seq     uint32 `json:"seq"`
	Type    uint32 `json:"type"`
	Chapter uint32 `json:"chapter"`
	Name    string `json:"name"`
	Seconds uint32 `json:"seconds"`
	Finish  int64  `json:"finish"`
}

type progAutoTicket struct {
	Type     uint32 `json:"type"`
	Count    uint32 `json:"count"`
	ExpireAt int64  `json:"expire_at"`
}

type progResearchTimer struct {
	Kind   string `json:"kind"`
	Key    uint32 `json:"key"`
	Name   string `json:"name"`
	Sub    string `json:"sub"`
	Time   int64  `json:"time"`
	Finish int64  `json:"finish"`
}

type progFleetTechTimer struct {
	Group  uint32 `json:"group"`
	Name   string `json:"name"`
	Level  int    `json:"level"`
	Time   int64  `json:"time"`
	Finish int64  `json:"finish"`
}

type progUntilTimer struct {
	Key   uint32 `json:"key"`
	Until int64  `json:"until"`
}

type progTimers struct {
	Now            int64                 `json:"now"`
	Builds         []progBuildTimer      `json:"builds"`
	BuildPoints    uint32                `json:"build_points"`
	BuildPointsMax uint32                `json:"build_points_max"`
	BuildSlots     uint32                `json:"build_slots"`
	Commissions    []progCommissionTimer `json:"commissions"`
	DailyDrawn     uint32                `json:"daily_drawn"`
	DailyDraws     uint32                `json:"daily_draws"`
	Lessons        []progLessonTimer     `json:"lessons"`
	Rooms          uint32                `json:"rooms"`
	QuickUsed      uint32                `json:"quick_used"`
	QuickAllowed   uint32                `json:"quick_allowed"`
	Fields         []progFieldTimer      `json:"fields"`
	AutoUsed       int64                 `json:"auto_used"`
	AutoLimit      int64                 `json:"auto_limit"`
	AutoTickets    []progAutoTicket      `json:"auto_tickets"`
	AutoBattles    []progAutoTimer       `json:"auto_battles"`
	Research       []progResearchTimer   `json:"research"`
	BlueprintCold  int64                 `json:"blueprint_cold"`
	FleetTech      []progFleetTechTimer  `json:"fleet_tech"`
	Cooldowns      []progUntilTimer      `json:"cooldowns"`
	GuildWait      int64                 `json:"guild_wait"`
}

// progShip is a ship on a timer, drawn in the skin it wears (0 for a ship
// being built).
func (server *Server) progShip(templateID, skinID uint32, name string) progTimerShip {
	template := server.names[dropShip][templateID]
	if name == "" {
		name = template.Name
	}
	return progTimerShip{Name: name, Rarity: template.Rarity, Icon: server.names.shipIcon(templateID, skinID)}
}

// handleTimers lists everything of the commander running on a clock.
func (server *Server) handleTimers(r *http.Request) (any, error) {
	commander, err := server.commander(r)
	if err != nil {
		return nil, err
	}
	tables, err := loadProgressTables()
	if err != nil {
		return nil, err
	}
	ctx, id, now := r.Context(), commander.ID, time.Now()
	out := progTimers{
		Now: now.Unix(), BuildPointsMax: game.BuildPointsMax(), BuildSlots: game.BuildSlots(), DailyDraws: game.CommissionDraws, Rooms: commander.SkillClassNum,
		Builds: []progBuildTimer{}, Commissions: []progCommissionTimer{}, Lessons: []progLessonTimer{}, Fields: []progFieldTimer{},
		AutoTickets: []progAutoTicket{}, AutoBattles: []progAutoTimer{}, Research: []progResearchTimer{}, FleetTech: []progFleetTechTimer{}, Cooldowns: []progUntilTimer{},
	}
	if out.QuickUsed, out.QuickAllowed, err = server.Game.QuickFinishes(ctx, commander, now); err != nil {
		return nil, err
	}

	builds, err := server.Store.ListBuilds(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, build := range builds {
		ship := server.progShip(build.TemplateID, 0, "")
		out.Builds = append(out.Builds, progBuildTimer{ID: build.ID, Ship: ship, Pool: tables.pools[build.PoolID].Name, Start: build.FinishTime - int64(build.Duration), Finish: build.FinishTime})
	}
	if out.BuildPoints, err = server.Store.GetBuildPoints(ctx, id); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out.BuildPoints = min(out.BuildPoints, out.BuildPointsMax)

	commissions, err := server.Store.ListCommissions(ctx, id)
	if err != nil {
		return nil, err
	}
	ships, err := server.Store.ProgressListCommissionShips(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, row := range commissions {
		if row.FinishTime == 0 {
			continue
		}
		config := tables.collections[row.CollectionID]
		timer := progCommissionTimer{ID: row.CollectionID, Name: config.Title, Finish: int64(row.FinishTime), Start: int64(row.FinishTime) - config.CollectTime, Ships: []progTimerShip{}}
		for _, ship := range ships {
			if ship.CollectionID == row.CollectionID {
				one := server.progShip(ship.TemplateID, ship.SkinID, ship.Name)
				one.ID = ship.ID
				timer.Ships = append(timer.Ships, one)
			}
		}
		out.Commissions = append(out.Commissions, timer)
	}
	if out.DailyDrawn, err = server.Store.GetCommissionDailyDrawn(ctx, id); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	lessons, err := server.Store.ProgressListLessons(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, lesson := range lessons {
		ship := server.progShip(lesson.TemplateID, lesson.SkinID, lesson.Name)
		ship.ID = lesson.ShipID
		out.Lessons = append(out.Lessons, progLessonTimer{
			Room: lesson.RoomID, Ship: ship, Skill: tables.skills[lesson.SkillID].Name, Book: server.names[dropItem][lesson.ItemID].Name,
			Start: lesson.StartTime, Finish: lesson.FinishTime,
		})
	}

	resources, err := server.Store.ListResources(ctx, id)
	if err != nil {
		return nil, err
	}
	owned := map[uint32]uint32{}
	for _, row := range resources {
		owned[row.ResourceID] = row.Amount
	}
	for _, field := range []struct {
		key, level, resource uint32
		upgradeAt            int64
	}{
		{game.FieldOil, commander.OilFieldLevel, game.ResourceOilField, commander.OilFieldUpgradeAt},
		{game.FieldGold, commander.GoldFieldLevel, game.ResourceGoldField, commander.GoldFieldUpgradeAt},
		{game.FieldClass, commander.ClassFieldLevel, game.ResourceClassField, commander.ClassFieldUpgradeAt},
	} {
		top, upgrade := game.FieldTable(field.key, field.level)
		row := progFieldTimer{Key: field.key, Level: field.level, MaxLevel: top, UpgradeAt: field.upgradeAt, Stored: owned[field.resource]}
		if field.upgradeAt > 0 {
			row.UpgradeTime = upgrade
		}
		out.Fields = append(out.Fields, row)
	}

	if out.AutoUsed, out.AutoLimit, err = game.AutoTime(ctx, server.Store.Queries, id, now); err != nil {
		return nil, err
	}
	tickets, err := server.Store.ListChapterAutoTickets(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, ticket := range tickets {
		out.AutoTickets = append(out.AutoTickets, progAutoTicket{Type: ticket.Type, Count: ticket.Count, ExpireAt: ticket.ExpireAt})
	}
	battles, err := server.Store.ListChapterAutoBattles(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, battle := range battles {
		out.AutoBattles = append(out.AutoBattles, progAutoTimer{
			Seq: battle.Seq, Type: battle.Type, Chapter: battle.ChapterID, Name: tables.stageName(battle.ChapterID),
			Seconds: battle.Seconds, Finish: battle.FinishAt,
		})
	}

	offers, err := server.Store.ListTechnologyOffers(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, offer := range offers {
		if offer.FinishAt == 0 {
			continue
		}
		project := tables.projects[offer.TechID]
		out.Research = append(out.Research, progResearchTimer{Kind: game.TimerResearch, Key: offer.Pos, Name: project.Name, Sub: project.SubName, Time: project.Time, Finish: offer.FinishAt})
	}
	queue, err := server.Store.ListTechnologyQueue(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, entry := range queue {
		project := tables.projects[entry.TechID]
		out.Research = append(out.Research, progResearchTimer{Kind: game.TimerQueue, Key: entry.ID, Name: project.Name, Sub: project.SubName, Time: project.Time, Finish: entry.FinishAt})
	}
	state, err := server.Store.GetBlueprintState(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out.BlueprintCold = state.ColdTime

	techs, err := server.Store.ListFleetTechs(ctx, id)
	if err != nil {
		return nil, err
	}
	groups, _, err := server.Game.FleetTech(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, tech := range techs {
		if tech.StudyTechID == 0 {
			continue
		}
		timer := progFleetTechTimer{Group: tech.GroupID, Name: tables.techGroups[tech.GroupID].Name, Finish: tech.StudyFinishAt}
		for _, group := range groups {
			if group.ID == tech.GroupID {
				timer.Level, timer.Time = group.Study, group.StudyTime
			}
		}
		out.FleetTech = append(out.FleetTech, timer)
	}

	cooldowns, err := server.Store.ListCooldowns(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, cooldown := range cooldowns {
		out.Cooldowns = append(out.Cooldowns, progUntilTimer{Key: cooldown.Key, Until: cooldown.Until})
	}
	wait, err := server.Store.GetGuildWaitTime(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out.GuildWait = int64(wait)
	return out, nil
}

// handleFinishTimer ends one running timer now: {kind, key}, kind one of
// game.Timer*.
func (server *Server) handleFinishTimer(r *http.Request) (any, error) {
	commander, err := server.commander(r)
	if err != nil {
		return nil, err
	}
	var body struct {
		Kind string `json:"kind"`
		Key  uint32 `json:"key"`
	}
	if err := readJSON(r, &body); err != nil {
		return nil, err
	}
	return kicked(server.Game.FinishTimer(r.Context(), commander.ID, body.Kind, body.Key))
}

// handleFinishAllTimers ends every running timer now.
func (server *Server) handleFinishAllTimers(r *http.Request) (any, error) {
	commander, err := server.commander(r)
	if err != nil {
		return nil, err
	}
	count, was, err := server.Game.FinishAllTimers(r.Context(), commander.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"finished": count, "kicked": was}, nil
}

// handleResetCounter sets a daily counter back to 0: {counter}, one of
// game.Counter*.
func (server *Server) handleResetCounter(r *http.Request) (any, error) {
	commander, err := server.commander(r)
	if err != nil {
		return nil, err
	}
	var body struct {
		Counter string `json:"counter"`
	}
	if err := readJSON(r, &body); err != nil {
		return nil, err
	}
	return kicked(server.Game.ResetCounter(r.Context(), commander.ID, body.Counter))
}

// handleBuildPoints sets the build points: {points}.
func (server *Server) handleBuildPoints(r *http.Request) (any, error) {
	commander, err := server.commander(r)
	if err != nil {
		return nil, err
	}
	var body struct {
		Points uint32 `json:"points"`
	}
	if err := readJSON(r, &body); err != nil {
		return nil, err
	}
	return kicked(server.Game.SetBuildPoints(r.Context(), commander.ID, body.Points))
}

// handleFieldLevel sets an academy field's level: {field, level}.
func (server *Server) handleFieldLevel(r *http.Request) (any, error) {
	commander, err := server.commander(r)
	if err != nil {
		return nil, err
	}
	var body struct {
		Field uint32 `json:"field"`
		Level uint32 `json:"level"`
	}
	if err := readJSON(r, &body); err != nil {
		return nil, err
	}
	return kicked(server.Game.SetFieldLevel(r.Context(), commander.ID, body.Field, body.Level))
}
