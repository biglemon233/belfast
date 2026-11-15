package game

import (
	"context"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/live"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// online is the connections of the commanders playing on this server, how
// one commander's request reaches another (a friend request, a private
// message, world chat).
type online struct {
	mu      sync.Mutex
	clients map[uint32]*connection.Client // by commander
}

// connect marks the client's commander online once the client can take
// pushes, after the login burst (CS_11001, also sent again by most
// reconnects: ConnectionMgr skips it when nothing was sent yet). The
// commander's friends online see it come online (SC_50008), and offline
// again when the connection closes.
func (game *Game) connect(ctx context.Context, client *connection.Client) error {
	id := client.CommanderID
	game.online.mu.Lock()
	if game.online.clients == nil {
		game.online.clients = make(map[uint32]*connection.Client)
	}
	known := game.online.clients[id] == client
	game.online.clients[id] = client
	game.online.mu.Unlock()
	if known {
		return nil
	}
	// the day's first login opens the comparison window the back-office
	// diffs against; a relogin later the same day keeps it.
	if err := game.CaptureSessionBaseline(ctx, id); err != nil {
		logger.WithFields("Session", logger.FieldValue("commander", id), logger.FieldValue("error", err)).Warn("login baseline not captured")
	}
	game.Live.Publish(live.Event{Kind: live.Online})
	client.OnClose(func() { game.disconnect(client, id) })
	if err := game.DB.SetLastOnline(ctx, db.SetLastOnlineParams{LastOnline: time.Now().Unix(), ID: id}); err != nil {
		return err
	}
	return game.tellFriends(ctx, client, id)
}

// disconnect marks the commander offline when its connection closes, unless
// another one already took over.
func (game *Game) disconnect(client *connection.Client, id uint32) {
	game.online.mu.Lock()
	if game.online.clients[id] != client {
		game.online.mu.Unlock()
		return
	}
	delete(game.online.clients, id)
	game.online.mu.Unlock()
	game.Live.Publish(live.Event{Kind: live.Online})
	ctx := context.Background()
	if err := game.DB.SetLastOnline(ctx, db.SetLastOnlineParams{LastOnline: time.Now().Unix(), ID: id}); err != nil {
		logger.WithFields("Friend", logger.FieldValue("commander", id), logger.FieldValue("error", err)).Warn("last online not saved")
	}
	if err := game.tellFriends(ctx, client, id); err != nil {
		logger.WithFields("Friend", logger.FieldValue("commander", id), logger.FieldValue("error", err)).Warn("friends not told")
	}
}

// tellFriends pushes the commander's FRIEND_INFO (online or not, last
// online time) to its friends online: FriendProxy updates them (SC_50008).
func (game *Game) tellFriends(ctx context.Context, client *connection.Client, id uint32) error {
	friends, err := game.DB.ListFriendIDs(ctx, id)
	if err != nil || len(friends) == 0 {
		return err
	}
	commander, err := game.DB.GetCommander(ctx, id)
	if err != nil {
		return err
	}
	update := &protobuf.SC_50008{Player: game.friendInfo(commander)}
	for _, friend := range friends {
		game.deliver(client, friend, update)
	}
	return nil
}

// onlineClient is the connection of a commander playing, nil when offline.
func (game *Game) onlineClient(id uint32) *connection.Client {
	game.online.mu.Lock()
	defer game.online.mu.Unlock()
	return game.online.clients[id]
}

// isOnline tells whether a commander is playing.
func (game *Game) isOnline(id uint32) bool {
	return game.onlineClient(id) != nil
}

// onlineCommanders lists the commanders playing.
func (game *Game) onlineCommanders() []uint32 {
	game.online.mu.Lock()
	defer game.online.mu.Unlock()
	ids := make([]uint32, 0, len(game.online.clients))
	for id := range game.online.clients {
		ids = append(ids, id)
	}
	return ids
}

// deliver pushes msgs to commander to if it is online, from the request
// client is handling: to the client itself ahead of its reply, to another
// commander's connection between two of its packets. It tells whether to was
// online; offline commanders get what they missed from the database at their
// next login.
func (game *Game) deliver(client *connection.Client, to uint32, msgs ...proto.Message) bool {
	target := game.onlineClient(to)
	if target == nil {
		return false
	}
	if target == client {
		for _, msg := range msgs {
			if err := client.Push(msg); err != nil {
				logger.WithFields("Friend", logger.FieldValue("commander", to), logger.FieldValue("error", err)).Warn("push failed")
			}
		}
		return true
	}
	err := target.Run(func(target *connection.Client) error {
		for _, msg := range msgs {
			if err := target.Push(msg); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logger.WithFields("Friend", logger.FieldValue("commander", to), logger.FieldValue("error", err)).Warn("push not delivered")
		return false
	}
	return true
}
