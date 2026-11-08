package game

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// Purchases with real money (controller/command/player/charge*.lua,
// ShopsProxy). The official server answers a purchase request with a pay id
// the client hands to the platform SDK (Google Play, Yostar's billing), which
// charges the account; the SDK then reports back through CS_11504 (paid) or
// CS_11510 (failed), and the gems come in SC_11505 / SC_11503. Belfast has no
// payment provider: no purchase ever starts, so there is never a pay id to
// confirm, fail or refund. Nothing of this was captured: the replies are
// built from the Lua alone.

// errChargeDisabled is SC_11502 / SC_11514 result 5002: ChargeCommand shows
// charge_error_disable ("The store payment processor is currently under
// maintenance."), RefundChargeCommand errorTip("charge", 5002).
var errChargeDisabled error = refusal{"charging disabled", 5002}

// errChargeLimit is SC_11502 result 6: ChargeCommand shows
// charge_error_count_limit ("Purchase limit reached"), the same result the
// SDK's own limit uses.
var errChargeLimit error = refusal{"charge limit reached", 6}

// handleCharge answers ChargeCommand (CS_11501, a gem pack, a gift pack or a
// pass bought with money).
//
// The official server answers with a pay id the client hands to the platform
// SDK, which charges the store account and reports back through CS_11504
// (paid) or CS_11510 (failed). Belfast has no payment provider, so it hands
// the goods out itself: the purchase is granted here (gems and
// extra_service_item, from server/pay_data.json), counted against its limit,
// and the client is told it succeeded through SC_11503, the push the official
// server sends once a payment went through (PlayerProxy 11503 →
// GAME.CHARGE_SUCCESS: the gems, the charged list and "charge_success").
// SC_11502 still answers result 0 so the client calls the SDK and its pay id
// feeds SC_11503's; the SDK's failure (CS_11510, no store account) is
// logged and changes nothing.
func (game *Game) handleCharge(client *connection.Client, req *protobuf.CS_11501) error {
	ctx, now := context.Background(), time.Now()
	good, ok := chargeGoods[req.GetShopId()]
	if !ok || !good.Time.Open(now) {
		logger.WithFields("Charge", logger.FieldValue("commander", client.CommanderID), logger.FieldValue("shop", req.GetShopId())).Info("purchase refused: no such goods on sale")
		return client.Send(&protobuf.SC_11502{Result: result(errChargeDisabled), PayId: proto.String(""), Url: proto.String(""), OrderSign: proto.String("")})
	}
	// the pay id the SDK failure and SC_11503 name; the client ignores a
	// repeat of the last one (charge_trade_no_error), so it is unique
	payID := fmt.Sprintf("%d-%d", client.CommanderID, now.UnixNano())
	gem, gemFree := good.Gem+good.ExtraGem, uint32(0)
	err := game.tx(ctx, client, func(tx *Tx) error {
		purchases, err := shopPurchases(tx.ctx, tx.Queries, tx.commanderID, now)
		if err != nil {
			return err
		}
		if good.LimitArg > 0 && purchases[req.GetShopId()] >= good.LimitArg {
			return errChargeLimit
		}
		// 首充双倍: the first purchase of a pack that offers it pays gem*2
		// instead of gem + extra_gem (ChargeCommand's SdkPay amount)
		if good.FirstPayDouble != 0 && purchases[req.GetShopId()] == 0 {
			gem = good.Gem * 2
		}
		if gem > 0 {
			if err := tx.AddResource(ResourceGem, gem); err != nil {
				return err
			}
		}
		if gemFree > 0 {
			if err := tx.AddResource(ResourceFreeGem, gemFree); err != nil {
				return err
			}
		}
		if len(good.Items) > 0 {
			drops := make([]*protobuf.DROPINFO, 0, len(good.Items))
			for _, item := range good.Items {
				drops = append(drops, dropInfo(item[0], item[1], item[2]))
			}
			if err := tx.grantDrops(drops); err != nil {
				return err
			}
		}
		return tx.AddShopPurchase(tx.ctx, db.AddShopPurchaseParams{
			CommanderID: tx.commanderID, ShopID: req.GetShopId(), ExpiresAt: good.purchaseResetAt(now), Count: 1,
		})
	})
	if refused(err) {
		return client.Send(&protobuf.SC_11502{Result: result(err), PayId: proto.String(""), Url: proto.String(""), OrderSign: proto.String("")})
	}
	if err != nil {
		return err
	}
	logger.WithFields("Charge", logger.FieldValue("commander", client.CommanderID), logger.FieldValue("shop", req.GetShopId()), logger.FieldValue("money", good.Money)).Info("purchase granted")
	if err := client.Send(&protobuf.SC_11502{Result: proto.Uint32(0), PayId: proto.String(payID), Url: proto.String(""), OrderSign: proto.String("")}); err != nil {
		return err
	}
	return client.Send(&protobuf.SC_11503{ShopId: proto.Uint32(req.GetShopId()), PayId: proto.String(payID), Gem: proto.Uint32(gem), GemFree: proto.Uint32(gemFree)})
}

// handleRefundCharge answers RefundChargeCommand (CS_11513, paying back a
// charged-back purchase from BackChargeScene, view/main/backchargescene.lua).
// Like ChargeCommand, result 0 calls the platform SDK's pay for real money,
// so it is always refused with 5002 (errorTip("charge", 5002), no SDK
// call). The scene only opens for a commander with refund info, which
// belfast never has (handleRefundInfo).
//
//belfast:static
func (game *Game) handleRefundCharge(client *connection.Client, req *protobuf.CS_11513) error {
	logger.WithFields("Charge", logger.FieldValue("commander", client.CommanderID), logger.FieldValue("shop", req.GetShopId()), logger.FieldValue("device", req.GetDevice())).Info("refund payment refused: no payment provider")
	return client.Send(&protobuf.SC_11514{Result: result(errChargeDisabled), PayId: proto.String(""), Url: proto.String(""), OrderSign: proto.String("")})
}

// handleChargeConfirm answers ChargeConfirmCommand (CS_11504), which the
// SDK triggers once a payment went through. No pay id belfast handed out
// exists, so it answers result 1: the client stops the pay id's retry
// timer (ShopsProxy:removeChargeTimer) and shows nothing. Result 4 would
// make it retry the confirmation forever (ShopsProxy:chargeFailed).
//
//belfast:static
func (game *Game) handleChargeConfirm(client *connection.Client, req *protobuf.CS_11504) error {
	logger.WithFields("Charge", logger.FieldValue("commander", client.CommanderID), logger.FieldValue("pay_id", req.GetPayId()), logger.FieldValue("order", req.GetPayIdBili())).Warn("payment confirmation for an unknown pay id")
	return client.Send(&protobuf.SC_11505{Result: proto.Uint32(resultFailed), ShopId: proto.Uint32(0), Gem: proto.Uint32(0), GemFree: proto.Uint32(0)})
}

// handleChargeFailed takes ChargeFailedCommand (CS_11510), the SDK's report
// of a payment that failed (code its error, made positive). There is no
// reply; belfast only logs it.
//
//belfast:static
func (game *Game) handleChargeFailed(client *connection.Client, req *protobuf.CS_11510) error {
	logger.WithFields("Charge", logger.FieldValue("commander", client.CommanderID), logger.FieldValue("pay_id", req.GetPayId()), logger.FieldValue("code", req.GetCode())).Warn("payment failure reported")
	return nil
}

// handleRefundInfo answers GetRefundInfoCommand (CS_11023, type 1): the
// purchases charged back and still to pay (PlayerProxy:setRefundInfo).
// MainRefundSequence asks for it at login when the gateway's
// limit_server_ids holds servers, and a non-empty list blocks the game
// behind the Supplement_pay1 box. Nothing is ever bought with money here,
// so the list is empty (as SC_11003.refund_shop_info_list).
//
//belfast:static
func (game *Game) handleRefundInfo(client *connection.Client, req *protobuf.CS_11023) error {
	return client.Send(&protobuf.SC_11024{Result: result(nil)})
}

// Tapping Akashi in the shop (ClickMingShiCommand) raises her affection,
// Player.chargeExp (SC_11003.acc_pay_lv, the pay_level_award level and
// dialogue): the client adds mingShiExp on success. The wiki (Akashi's Shop,
// "Akashi's Gifts") says taps raise it at most twice a day; the client itself
// stops sending after two taps or a refusal until its next login
// (Player.mingshiflag starts at 0).
const (
	mingShiExp       = 5
	mingShiDailyTaps = 2
)

// errMingShiLimit refuses a tap past the day's: the client sets mingshiflag
// to 2 and shows nothing.
var errMingShiLimit error = refusal{"no more taps today", resultFailed}

// chargeExp is the commander's SC_11003.acc_pay_lv.
func chargeExp(ctx context.Context, queries *db.Queries, commanderID uint32) (uint32, error) {
	row, err := queries.GetChargeExp(ctx, commanderID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return row.Exp, err
}

// handleClickMingShi answers ClickMingShiCommand (CS_11506, state 0): a tap
// on Akashi in the shop adds mingShiExp affection, up to mingShiDailyTaps a
// server day. The affection gifts the wiki lists for some levels are not
// in the client's tables and are not sent.
func (game *Game) handleClickMingShi(client *connection.Client, req *protobuf.CS_11506) error {
	ctx, now := context.Background(), time.Now()
	err := game.tx(ctx, client, func(tx *Tx) error {
		row, err := tx.GetChargeExp(ctx, client.CommanderID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !sameServerDay(time.Unix(row.TappedAt, 0), now) {
			row.Taps = 0
		}
		if row.Taps >= mingShiDailyTaps {
			return errMingShiLimit
		}
		return tx.SaveChargeExp(ctx, db.SaveChargeExpParams{CommanderID: client.CommanderID, Exp: row.Exp + mingShiExp, Taps: row.Taps + 1, TappedAt: now.Unix()})
	})
	if err != nil && !refused(err) {
		return err
	}
	return client.Send(&protobuf.SC_11507{Result: result(err)})
}
