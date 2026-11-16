package game

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/gamedata"
)

// newServerBuild is server/newserver_build.json, the newbie build pool
// (ship_data_create_material 11, 庆典新手建造). The pool's roster isn't in a
// table the server can read (ship_data_group only marks the permanent pools),
// so it is kept here: the out-of-print ships of the community wiki's list
// (绝版舰娘一览, the META ones left out, since those are the ones a new
// commander can't get any other way) and the ticket each build costs.
type newServerBuild struct {
	Pool   uint32   `json:"pool"`
	Ticket uint32   `json:"ticket"`
	Ships  []uint32 `json:"ships"`
	// the rarity rates in percent (6 = ultra rare, 5 = super rare, 4 = elite,
	// 3 = rare, 2 = common), by rarity; the pool's own rate_tip when empty
	Rates map[uint32]float64 `json:"rates"`
	// the builds after which the pool's ship is given (累计建造 40 次)
	MilestoneCount uint32 `json:"milestone_count"`
	MilestoneShip  uint32 `json:"milestone_ship"`
}

var newServerBuilds *newServerBuild

func loadNewServerBuild() error {
	raw, err := gamedata.ServerFile("newserver_build.json")
	if raw == nil || err != nil {
		return err
	}
	var config newServerBuild
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("new server build: %w", err)
	}
	newServerBuilds = &config
	return nil
}

// newServerBuildActivity is the new-server carnival activity whose data3
// counts the newbie pool's builds (the client shows them as 累计建造 N/40).
// It is the activity the 9.7.434 data has; a client on newer tables shows its
// own counter, the ship is given all the same.
const newServerBuildActivity = 8120

// spendNewServerTickets takes count 新手单次建造券 for a newbie pool build.
func (tx *Tx) spendNewServerTickets(count uint32) error {
	taken, err := tx.SpendVirtualItem(tx.ctx, db.SpendVirtualItemParams{
		CommanderID: tx.commanderID,
		ItemID:      newServerBuilds.Ticket,
		Count:       count,
	})
	if err != nil {
		return err
	}
	if taken == 0 {
		return ErrNotEnough
	}
	return nil
}

// newServerBuildMilestone counts the newbie pool's builds and gives its ship
// at the milestone (累计建造 40 次), once.
func (tx *Tx) newServerBuildMilestone(count uint32) error {
	config, ok := activityConfigs[newServerBuildActivity]
	if !ok || newServerBuilds.MilestoneCount == 0 || newServerBuilds.MilestoneShip == 0 {
		return nil
	}
	state, err := activityState(tx.ctx, tx.Queries, tx.commanderID, config)
	if err != nil {
		return err
	}
	state.Data3 += count
	if state.Data3 >= newServerBuilds.MilestoneCount && state.Data4 == 0 {
		if _, err := tx.AddShip(newServerBuilds.MilestoneShip); err != nil {
			return err
		}
		state.Data4 = 1
	}
	return tx.Queries.SaveActivity(tx.ctx, db.SaveActivityParams(state))
}

// poolRates is a build pool's rarity rates from its rate_tip: the first rate
// of each rarity, the Ultra Rare rate of a pool without one going to Common
// (the wiki's Building page, as the pools without URs show in their rate_tip:
// Common 30% where those with URs have 28.8%).
func poolRates(pool buildPool) ([]rarityRate, float64) {
	tipRates := map[uint32]float64{}
	for _, tip := range pool.RateTip {
		match := rateTipLine.FindStringSubmatch(tip)
		if match == nil {
			continue
		}
		rate, err := strconv.ParseFloat(match[2], 64)
		rarity, named := uint32(shipRarityGray), true
		if match[1] != "" {
			rarity, named = rarityByRateColor[strings.ToLower(match[1])]
		}
		if _, seen := tipRates[rarity]; err != nil || !named || seen {
			continue
		}
		tipRates[rarity] = rate
	}
	if len(pool.candidates[shipRarityUR]) == 0 {
		tipRates[shipRarityGray] += tipRates[shipRarityUR]
		delete(tipRates, shipRarityUR)
	}
	var rates []rarityRate
	total := 0.0
	for rarity, rate := range tipRates {
		if len(pool.candidates[rarity]) > 0 && rate > 0 {
			rates = append(rates, rarityRate{rarity, rate})
			total += rate
		}
	}
	return rates, total
}
