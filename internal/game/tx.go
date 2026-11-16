package game

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/live"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// Tx is a request's unit of work: its queries run in one transaction, and
// what they cause besides storage is collected on it. The inventory
// primitives (SpendResource, AddItem, AddShip, grantDrops...) record the task
// events and pushes they cause themselves; a handler only adds the events
// about the action itself (a build, a win...).
type Tx struct {
	*db.Queries
	ctx         context.Context
	commanderID uint32

	events   []TaskEvent
	after    []func() error
	pushes   []proto.Message      // SC_17002 archive updates... in the order they happened
	newShips []*protobuf.SHIPINFO // ships obtained outside of a reply, pushed in SC_12042

	// newShipsAfterReply sends the SC_12042 after the reply instead of
	// before it (the battle result, as captured).
	newShipsAfterReply bool

	mailSent bool // SendMail queued the SC_30001 push

	// storeChanged is set when oil spilled into the mail storage: its amount
	// isn't in any reply, so the client gets the resources again (SC_11004)
	storeChanged bool

	techPoints bool // techPointsChanged: count the Fleet Technology points again

	ownIsland *islandState // the commander's island once Tx.island loaded it
}

// tx runs fn in a transaction (see commit). When it commits, the task
// progress pushes (SC_20002, SC_20102, SC_20103, SC_20202) are queued on the
// client, then the other pushes, then the new ships (SC_12042), all ahead of
// the reply. Nothing is queued when it fails.
func (game *Game) tx(ctx context.Context, client *connection.Client, fn func(tx *Tx) error) error {
	tx, progress, err := game.commit(ctx, client.CommanderID, fn)
	if err != nil {
		return err
	}
	for _, push := range append(progress, tx.pushes...) {
		if err := client.Push(push); err != nil {
			return err
		}
	}
	if len(tx.newShips) == 0 {
		return nil
	}
	// BayProxy adds them
	newShips := &protobuf.SC_12042{ShipList: tx.newShips}
	if tx.newShipsAfterReply {
		return client.PushAfterReply(newShips)
	}
	return client.Push(newShips)
}

// commit runs fn on the commander's state in a transaction, without a
// client to push to. Once fn succeeds the Fleet Technology points and the
// task events it recorded are applied in the same transaction, then its
// afterEvents functions run and the island it loaded is saved. When it
// commits, the back-office is told the commander changed. It returns the
// unit of work and the task progress pushes, for the caller to send.
func (game *Game) commit(ctx context.Context, commanderID uint32, fn func(tx *Tx) error) (*Tx, []proto.Message, error) {
	var tx *Tx
	var progress []proto.Message
	err := game.DB.InTx(ctx, func(queries *db.Queries) error {
		tx = newTx(ctx, queries, commanderID)
		if err := fn(tx); err != nil {
			return err
		}
		if err := tx.recordTechPoints(); err != nil {
			return err
		}
		var err error
		if progress, err = game.applyTaskEvents(ctx, queries, commanderID, tx.events); err != nil {
			return err
		}
		for _, fn := range tx.after {
			if err := fn(); err != nil {
				return err
			}
		}
		if tx.storeChanged {
			// the storage's oil and gold aren't in a gain's reply
			rows, err := queries.ListResources(ctx, commanderID)
			if err != nil {
				return err
			}
			tx.pushes = append(tx.pushes, &protobuf.SC_11004{ResourceList: resourceList(rows)})
		}
		return tx.saveOwnIsland()
	})
	if err != nil {
		return nil, nil, err
	}
	game.Live.Publish(live.Event{Kind: live.Commander, ID: commanderID})
	return tx, progress, nil
}

// AdminWrite runs fn on a commander's state for the back-office: the
// client keeps its own copy of what fn changes, so a commander playing is
// kicked first (it sees the change at its next login), then fn runs as a
// request's would (see commit). It tells whether the commander was kicked.
func (game *Game) AdminWrite(ctx context.Context, commanderID uint32, fn func(tx *Tx) error) (bool, error) {
	kicked, err := game.Kick(commanderID)
	if err != nil {
		return kicked, err
	}
	_, _, err = game.commit(ctx, commanderID, fn)
	return kicked, err
}

// newTx is a unit of work on queries for commanderID; what it records is only
// applied when it runs through Game.commit.
func newTx(ctx context.Context, queries *db.Queries, commanderID uint32) *Tx {
	return &Tx{Queries: queries, ctx: ctx, commanderID: commanderID}
}

// event records what the commander did, for the tasks that count it.
func (tx *Tx) event(events ...TaskEvent) {
	tx.events = append(tx.events, events...)
}

// afterEvents runs fn in the transaction once the task events are applied,
// for what must not count them.
func (tx *Tx) afterEvents(fn func() error) {
	tx.after = append(tx.after, fn)
}

// push queues a message to send ahead of the reply once tx commits.
func (tx *Tx) push(msg proto.Message) {
	tx.pushes = append(tx.pushes, msg)
}
