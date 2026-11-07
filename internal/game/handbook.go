package game

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/connection"
	"github.com/ggmolly/belfast/internal/db"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// handbookLoginPush is SC_22300, CommanderManualProxy's state: the pages the
// commander got tasks from with their pt (one per finished page task) and
// the pt rewards claimed, and every commander manual task they finished (the
// client checks page and unlock tasks against that one list,
// CommanderManualPage.finishedTaskIds).
func (game *Game) handbookLoginPush(ctx context.Context, commander db.Commander) (*protobuf.SC_22300, error) {
	pages, err := game.DB.ListHandbookPages(ctx, commander.ID)
	if err != nil {
		return nil, err
	}
	finished, err := game.DB.ListHandbookFinished(ctx, commander.ID)
	if err != nil {
		return nil, err
	}
	msg := &protobuf.SC_22300{}
	byPage := map[uint32][]uint32{}
	seen := map[uint32]bool{}
	for _, task := range finished {
		byPage[task.PageID] = append(byPage[task.PageID], task.TaskID)
		if !seen[task.TaskID] {
			seen[task.TaskID] = true
			msg.FinishedTaskIds = append(msg.FinishedTaskIds, task.TaskID)
		}
	}
	for _, page := range pages {
		id := page.PageID
		pt := handbookPageConfigs[id].pt(byPage[id])
		msg.Handbooks = append(msg.Handbooks, &protobuf.TUTHANDBOOK{Id: proto.Uint32(id), Pt: proto.Uint32(pt), Award: proto.Uint32(page.Award), FinishedTaskIds: byPage[id]})
	}
	return msg, nil
}

// pt is the page's pt: its finished page tasks (task_list[i][0]).
func (page handbookPageConfig) pt(finished []uint32) uint32 {
	pt := uint32(0)
	for _, task := range finished {
		if page.isPageTask(task) {
			pt++
		}
	}
	return pt
}

// isPageTask tells whether taskID is one of the page's tasks
// (task_list[i][0]), the ones that earn it a pt.
func (page handbookPageConfig) isPageTask(taskID uint32) bool {
	for _, entry := range page.TaskList {
		if len(entry) > 0 && slices.Contains(entry[0], taskID) {
			return true
		}
	}
	return false
}

// handbookTaskClaimed records a claimed task on the commander manual pages
// it belongs to: a page task finishes and earns the page a pt
// (CommanderManualProxy.ShowTaskSubmitCall) and counts for the phase unlock
// tasks (sub_type 114, target the page); a task that unlocks a page or a
// page's tasks (unlock, task_list[i][1]) only finishes.
func handbookTaskClaimed(ctx context.Context, queries *db.Queries, commanderID uint32, taskID uint32) ([]TaskEvent, error) {
	var events []TaskEvent
	for _, page := range handbookTaskPages[taskID] {
		if err := queries.CreateHandbookPage(ctx, db.CreateHandbookPageParams{CommanderID: commanderID, PageID: page.page}); err != nil {
			return nil, err
		}
		added, err := queries.AddHandbookFinished(ctx, db.AddHandbookFinishedParams{CommanderID: commanderID, PageID: page.page, TaskID: taskID})
		if err != nil {
			return nil, err
		}
		if page.pageTask && added > 0 {
			events = append(events, TaskEvent{SubType: TaskSubHandbookPhase, Target: page.page, Count: 1})
		}
	}
	return events, nil
}

// handleHandbookGetTasks gives the tasks of a commander manual page entry
// (CommanderManualCommand GET_TASK: id is the tutorial_handbook_task page,
// index the 1-based task_list entry), pushed as SC_20003 before the reply.
// The capture lists them backwards.
func (game *Game) handleHandbookGetTasks(client *connection.Client, req *protobuf.CS_22302) error {
	ctx := context.Background()
	page, ok := handbookPageConfigs[req.GetId()]
	index := int(req.GetIndex()) - 1
	if !ok || index < 0 || index >= len(page.TaskList) || len(page.TaskList[index]) == 0 {
		return client.Send(&protobuf.SC_22303{Result: result(errRefused)})
	}
	tasks := slices.Clone(page.TaskList[index][0])
	slices.Reverse(tasks)
	var added *protobuf.SC_20003
	err := game.DB.InTx(ctx, func(queries *db.Queries) error {
		if err := queries.CreateHandbookPage(ctx, db.CreateHandbookPageParams{CommanderID: client.CommanderID, PageID: page.ID}); err != nil {
			return err
		}
		var err error
		added, err = acceptTasks(ctx, queries, client.CommanderID, tasks, uint32(time.Now().Unix()))
		return err
	})
	if err != nil {
		return err
	}
	// the page's state tasks start from what the commander already has
	if err := game.tx(ctx, client, func(tx *Tx) error { return tx.syncStateTasks() }); err != nil {
		return err
	}
	if added != nil {
		if err := client.Push(added); err != nil {
			return err
		}
	}
	return client.Send(&protobuf.SC_22303{Result: result(nil)})
}

// handleHandbookPtAward claims the next pt reward of a commander manual page
// (CommanderManualCommand GET_PT_AWARD, id the tutorial_handbook_task page):
// with award rewards claimed, the next one needs target[award] pt and gives
// drop_client[award] (CommanderManualPage.GetCurrentPtTarget /
// GetCurrentPtAward, the get_btn of CommanderManualLayer.SetPtPanel). The
// client then counts it (AddPageAward) and shows drop_list. No capture has
// it: built from the Lua alone.
func (game *Game) handleHandbookPtAward(client *connection.Client, req *protobuf.CS_22304) error {
	var awards []*protobuf.DROPINFO
	err := game.tx(context.Background(), client, func(tx *Tx) error {
		page, ok := handbookPageConfigs[req.GetId()]
		if !ok {
			return errRefused
		}
		award, err := tx.GetHandbookPageAward(tx.ctx, db.GetHandbookPageAwardParams{CommanderID: tx.commanderID, PageID: page.ID})
		if errors.Is(err, sql.ErrNoRows) {
			return errRefused
		}
		if err != nil {
			return err
		}
		if int(award) >= len(page.Target) || int(award) >= len(page.DropClient) {
			return errRefused
		}
		finished, err := tx.ListHandbookPageFinished(tx.ctx, db.ListHandbookPageFinishedParams{CommanderID: tx.commanderID, PageID: page.ID})
		if err != nil {
			return err
		}
		if page.pt(finished) < page.Target[award] {
			return errRefused
		}
		claimed, err := tx.ClaimHandbookPageAward(tx.ctx, db.ClaimHandbookPageAwardParams{CommanderID: tx.commanderID, PageID: page.ID, Award: award})
		if err != nil {
			return err
		}
		if claimed == 0 {
			return errRefused
		}
		awards = configDrops(page.DropClient[award])
		return tx.grantDrops(awards)
	})
	if err != nil && !refused(err) {
		return err
	}
	if err != nil {
		awards = nil
	}
	return client.Send(&protobuf.SC_22305{Result: result(err), DropList: awards})
}
