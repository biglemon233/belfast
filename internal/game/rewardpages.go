package game

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// ActivityConst.ACTIVITY_TYPE_LEVELAWARD and ACTIVITY_TYPE_STORY_AWARD: the
// new commander activities' reward pages (LevelAwardPage, StoryAwardPage).
// Both claim with CS_11202 cmd 1 and the row's id in arg 1 (the level from
// the level page's front_drops, the chapter from the chapter page), and keep
// the claimed ones in data1_list, which the pages read back.
const (
	activityTypeLevelAward = 4
	activityTypeStoryAward = 59
)

// levelAwardConfig is an activity_level_award row (LevelAwardPage): each
// front_drops entry is {level, {type, id, count}, ...} — the rewards a
// commander at that level may claim.
type levelAwardConfig struct {
	FrontDrops [][]json.RawMessage `json:"front_drops"`

	rows []levelAwardRow
}

// levelAwardRow is one front_drops entry.
type levelAwardRow struct {
	Level uint32
	Drops [][]uint32
}

// row is the entry for a level threshold.
func (config levelAwardConfig) row(level uint32) (levelAwardRow, bool) {
	for _, row := range config.rows {
		if row.Level == level {
			return row, true
		}
	}
	return levelAwardRow{}, false
}

// chapterAwardConfig is an activity_event_chapter_award row (StoryAwardPage):
// the chapters whose clear pays, and each one's award_display at its index.
type chapterAwardConfig struct {
	Chapter      []uint32     `json:"chapter"`
	AwardDisplay [][][]uint32 `json:"award_display"`
}

var (
	levelAwards   map[uint32]levelAwardConfig
	chapterAwards map[uint32]chapterAwardConfig
)

func loadRewardPageData() error {
	var levels map[uint32]levelAwardConfig
	var chapters map[uint32]chapterAwardConfig
	err := gamedata.LoadAll(
		gamedata.Load("activity_level_award", &levels),
		gamedata.Load("activity_event_chapter_award", &chapters),
	)
	if err != nil {
		return err
	}
	for id, config := range levels {
		rows := make([]levelAwardRow, 0, len(config.FrontDrops))
		for _, entry := range config.FrontDrops {
			if len(entry) == 0 {
				continue
			}
			var level uint32
			if json.Unmarshal(entry[0], &level) != nil {
				continue
			}
			var drops [][]uint32
			for _, drop := range entry[1:] {
				var spec []uint32
				if json.Unmarshal(drop, &spec) == nil && len(spec) >= 3 {
					drops = append(drops, spec)
				}
			}
			rows = append(rows, levelAwardRow{Level: level, Drops: drops})
		}
		config.rows = rows
		levels[id] = config
	}
	levelAwards, chapterAwards = levels, chapters
	return nil
}

// activityLevelAwardOperation claims a level reward (LevelAwardPage, type 4:
// activity 4, config_id 1): cmd 1's arg 1 is the level threshold; the
// commander's level has to have reached it, and data1_list keeps the claimed
// ones.
func (game *Game) activityLevelAwardOperation(client *connection.Client, config activityConfig, req *protobuf.CS_11202) error {
	if req.GetCmd() != 1 {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	level := req.GetArg1()
	row, ok := levelAwards[config.ConfigID].row(level)
	if !ok {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	ctx := context.Background()
	commander, err := game.DB.GetCommander(ctx, client.CommanderID)
	if err != nil {
		return err
	}
	if commander.Level < row.Level {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	reply := &protobuf.SC_11203{Result: result(nil)}
	err = game.tx(ctx, client, func(tx *Tx) error {
		state, err := activityState(ctx, tx.Queries, client.CommanderID, config)
		if err != nil {
			return err
		}
		if slices.Contains(state.Data1List, level) {
			return errRefused
		}
		drops := configDrops(row.Drops)
		if err := tx.grantDrops(drops); err != nil {
			return err
		}
		state.Data1List = append(state.Data1List, level)
		if err := tx.Queries.SaveActivity(ctx, db.SaveActivityParams(state)); err != nil {
			return err
		}
		reply.AwardList = drops
		return nil
	})
	if refused(err) {
		return client.Send(&protobuf.SC_11203{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}

// activityStoryAwardOperation claims a chapter's clear reward (StoryAwardPage,
// type 59: activity 368, config_id 368): cmd 1's arg 1 is the chapter, which
// has to have been cleared (its boss killed); data1_list keeps the claimed
// chapters.
func (game *Game) activityStoryAwardOperation(client *connection.Client, config activityConfig, req *protobuf.CS_11202) error {
	if req.GetCmd() != 1 {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	chapter := req.GetArg1()
	page, ok := chapterAwards[config.ConfigID]
	index := slices.Index(page.Chapter, chapter)
	if !ok || index < 0 || index >= len(page.AwardDisplay) {
		return client.Send(&protobuf.SC_11203{Result: result(errRefused)})
	}
	ctx := context.Background()
	reply := &protobuf.SC_11203{Result: result(nil)}
	err := game.tx(ctx, client, func(tx *Tx) error {
		state, err := activityState(ctx, tx.Queries, client.CommanderID, config)
		if err != nil {
			return err
		}
		if slices.Contains(state.Data1List, chapter) {
			return errRefused
		}
		progress, err := chapterProgress(ctx, tx.Queries, client.CommanderID, chapter)
		if err != nil {
			return err
		}
		if progress.KillBossCount == 0 {
			return errRefused
		}
		drops := configDrops(page.AwardDisplay[index])
		if err := tx.grantDrops(drops); err != nil {
			return err
		}
		state.Data1List = append(state.Data1List, chapter)
		if err := tx.Queries.SaveActivity(ctx, db.SaveActivityParams(state)); err != nil {
			return err
		}
		reply.AwardList = drops
		return nil
	})
	if refused(err) {
		return client.Send(&protobuf.SC_11203{Result: result(err)})
	}
	if err != nil {
		return err
	}
	return client.Send(reply)
}
