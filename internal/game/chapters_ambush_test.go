package game

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/protobuf"
)

// TestSpringAmbushLoopMode checks a loop mode (周回) chapter springs no ambush:
// the client gates its own rolls on not isLoop(), but it shows a prompt for any
// ambush cell the server sends, which stops Auto-Fight until it is tapped.
func TestSpringAmbushLoopMode(t *testing.T) {
	newSession := func(loop uint32) *chapterSession {
		return &chapterSession{
			template: &chapterTemplate{
				isAmbush: true,
				ambushes: []uint32{101210},
				// +20000 makes every cell certain, so a roll can't hide the
				// guard this test is about
				ambushRatios: []ambushRatio{{anyCell: true, ratio: 20000}},
			},
			info: &protobuf.CURRENTCHAPTERINFO{
				LoopFlag: proto.Uint32(loop),
				CellList: []*protobuf.CHAPTERCELLINFO_P13{{
					Pos: cellPos(2, 3),
				}},
			},
		}
	}
	session := newSession(0)
	cell := session.springAmbush(2, 3)
	if cell == nil || cell.GetItemType() != attachAmbush || cell.GetItemFlag() != cellFlagAmbush {
		t.Fatalf("a normal chapter should spring: %v", cell)
	}
	if len(session.ambushed) != 1 {
		t.Fatalf("sprung cells remembered: %v", session.ambushed)
	}

	loop := newSession(1)
	if cell := loop.springAmbush(2, 3); cell != nil {
		t.Fatalf("loop mode sprang an ambush: %v", cell)
	}
	if got := loop.info.CellList[0].GetItemType(); got != attachNone {
		t.Fatalf("loop mode changed the cell: item_type %d", got)
	}
}
