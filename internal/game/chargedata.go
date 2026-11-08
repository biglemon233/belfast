package game

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ggmolly/belfast/internal/gamedata"
)

// Money purchases (商店 → 茗的精选 and the diamond / pass / gift packs) are
// described only in the client's pay_data_display table: the shop_template
// has no row for them. The server needs the same rows to hand a purchase out
// itself, so tools/gen-pay-data.py exports the fields belfast reads into
// data/<region>/server/pay_data.json.

// chargeGood is a pay_data_display row, the fields a purchase uses.
type chargeGood struct {
	Money    uint32 `json:"money"`
	Gem      uint32 `json:"gem"`
	ExtraGem uint32 `json:"extra_gem"`
	// ExtraService is Goods' extra service: 3 a gift box, 4 a pass item, 6 a
	// skin; 0 a plain gem pack.
	ExtraService uint32 `json:"extra_service"`
	// LimitArg is how many times the goods can be bought, 0 without limit
	LimitArg uint32 `json:"limit_arg"`
	// LimitType is what resets that count: 4 a day (每日可购), the others
	// keep it for good
	LimitType      uint32          `json:"limit_type"`
	FirstPayDouble uint32          `json:"first_pay_double"`
	Time           gamedata.Window `json:"time"`
	// Items is what the purchase gives, extra_service_item entries
	// [type, id, count] (DROP_TYPE_*), besides the gems
	Items [][3]uint32 `json:"extra_service_item"`
}

// chargePurchaseResetDay is the LimitType of the goods that can be bought
// again the next server day.
const chargePurchaseResetDay = 4

var chargeGoods map[uint32]chargeGood

// loadChargeGoods reads server/pay_data.json; a region without it has no
// money purchases (every charge is refused).
func loadChargeGoods() error {
	raw, err := gamedata.ServerFile("pay_data.json")
	if err != nil {
		return err
	}
	chargeGoods = map[uint32]chargeGood{}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &chargeGoods); err != nil {
		return fmt.Errorf("server/pay_data.json: %w", err)
	}
	return nil
}

// chargePrice is what a purchase adds to the commander: the gems it sells,
// Gem of them bought and GemFree free ones (SC_11503.gem / gem_free, which
// the client adds itself too).
func (good chargeGood) price() (gem, gemFree uint32) {
	return good.Gem + good.ExtraGem, 0
}

// purchaseResetAt is when the goods' purchase count starts over (0 never).
func (good chargeGood) purchaseResetAt(now time.Time) int64 {
	if good.LimitType == chargePurchaseResetDay {
		return nextDay(now).Unix()
	}
	return 0
}
