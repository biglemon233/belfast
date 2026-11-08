package game

import (
	"math/rand/v2"

	"google.golang.org/protobuf/proto"

	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// rollPlaceholders opens the virtual placeholders in drops: "未知教材",
// "未知科技箱" and the like (item_virtual_data_statistics rows with a pool)
// are random boxes whose contents the official server draws when they are
// granted; the client has no way to use them (ItemInfoPanel shows only their
// count), so an unconverted one is stuck in the depot.
//
// The entry is rewritten in place, one draw for the whole stack, so the
// reply's award list carries the item that was actually granted.
func rollPlaceholders(drops []*protobuf.DROPINFO) {
	for _, entry := range drops {
		if entry.GetType() != dropTypeItem {
			continue
		}
		pool := dropPools[entry.GetId()]
		if len(pool) == 0 {
			continue
		}
		pick := pool[rand.IntN(len(pool))]
		logger.WithFields("Drops",
			logger.FieldValue("placeholder", entry.GetId()),
			logger.FieldValue("item", pick.id),
			logger.FieldValue("count", entry.GetNumber()),
		).Debug("placeholder opened")
		entry.Type = proto.Uint32(pick.kind)
		entry.Id = proto.Uint32(pick.id)
	}
}
