package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/game"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// serverLoginRejected is any non-zero result the client doesn't special-case
// (serverlogincommand.lua): it shows the generic server login failure.
const serverLoginRejected = 1

// trustClientAccount accepts the account id a client presents without a
// ticket belfast issued. Only the bilibili (CN) client needs it: its login is
// signed by bilibili's account service, which belfast can't verify. Local
// single-player use only — never expose such a server to other machines.
var trustClientAccount = os.Getenv("BELFAST_TRUST_CLIENT_ACCOUNT") == "1"

// handleServerLogin is the game server side of the login: the client presents
// the ticket from SC_10021 (or the one from a previous SC_10023 when it
// reconnects) and gets a fresh one back.
//
// user_id is the account's commander, 0 when it has none yet: the client then
// plays the prologue and goes to the create player scene (CS_10024).
func (gateway *Gateway) handleServerLogin(client *connection.Client, req *protobuf.CS_10022) error {
	account, ok := gateway.account(req.GetServerTicket())
	if !ok || account != req.GetAccountId() {
		if !trustClientAccount || req.GetAccountId() == 0 {
			logger.LogEvent("Gateway", "ServerLogin", fmt.Sprintf("rejected account %d from %s: unknown ticket", req.GetAccountId(), client.IP), logger.LOG_LEVEL_WARN)
			return client.Send(&protobuf.SC_10023{
				Result:       proto.Uint32(serverLoginRejected),
				UserId:       proto.Uint32(0),
				ServerTicket: proto.String(""),
			})
		}
		logger.LogEvent("Gateway", "ServerLogin", fmt.Sprintf("trusting client account %d from %s (BELFAST_TRUST_CLIENT_ACCOUNT=1)", req.GetAccountId(), client.IP), logger.LOG_LEVEL_WARN)
		account = req.GetAccountId()
	}
	commander, err := gateway.DB.GetCommanderByAccount(context.Background(), account)
	switch {
	case err == nil:
		client.CommanderID = commander.ID
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	client.AccountID = account
	logger.LogEvent("Gateway", "ServerLogin", fmt.Sprintf("account %d (commander %d) on server %d from %s", account, client.CommanderID, req.GetServerid(), client.IP), logger.LOG_LEVEL_INFO)
	return client.Send(&protobuf.SC_10023{
		Result:       proto.Uint32(0),
		UserId:       proto.Uint32(client.CommanderID),
		ServerTicket: proto.String(gateway.issueTicket(account)),
	})
}

// handleCreatePlayer creates the commander after the prologue (CS_10024).
// The client then loads the player data (CS_11001) as a new player.
func (gateway *Gateway) handleCreatePlayer(client *connection.Client, req *protobuf.CS_10024) error {
	if client.AccountID == 0 {
		return fmt.Errorf("CS_10024 before server login")
	}
	ctx := context.Background()
	commander, err := gateway.DB.GetCommanderByAccount(ctx, client.AccountID)
	switch {
	case err == nil:
		logger.LogEvent("Gateway", "CreatePlayer", fmt.Sprintf("account %d already has commander %d", client.AccountID, commander.ID), logger.LOG_LEVEL_WARN)
	case errors.Is(err, sql.ErrNoRows):
		err = gateway.DB.InTx(ctx, func(queries *db.Queries) error {
			commander, err = queries.CreateCommander(ctx, db.CreateCommanderParams{
				AccountID:    client.AccountID,
				Name:         req.GetNickName(),
				StarterShip:  req.GetShipId(),
				RegisteredAt: time.Now().Unix(),
			})
			if err != nil {
				return err
			}
			return game.StartCommander(ctx, queries, commander)
		})
		if err != nil {
			return err
		}
		logger.LogEvent("Gateway", "CreatePlayer", fmt.Sprintf("commander %d %q (ship %d) for account %d", commander.ID, commander.Name, commander.StarterShip, client.AccountID), logger.LOG_LEVEL_INFO)
	default:
		return err
	}
	client.CommanderID = commander.ID
	return client.Send(&protobuf.SC_10025{
		Result: proto.Uint32(0),
		UserId: proto.Uint32(commander.ID),
	})
}

// handleHeartbeat answers the client's ping (ConnectionMgr:resetHBTimer),
// sent on every connection; the client only times the reply, state is unused.
// Unanswered, it resends forever and eventually reconnects.
//
//belfast:static
func (gateway *Gateway) handleHeartbeat(client *connection.Client, req *protobuf.CS_10100) error {
	return client.Send(&protobuf.SC_10101{State: proto.Uint32(0)})
}
