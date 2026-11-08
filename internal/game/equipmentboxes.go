package game

import (
	"encoding/json"
	"fmt"

	"github.com/ggmolly/belfast/internal/gamedata"
	"github.com/ggmolly/belfast/internal/logger"
	"github.com/ggmolly/belfast/internal/protobuf"
)

// equipmentBoxes maps a gift box item to the equipment it gives, one
// [equip id, count] per entry. The ¥30 starter pack's 69001 装备补给箱T3 is
// one: unlike the random placeholders it has no bucket in the client data,
// its description lists the +6 gear instead, so the list is generated from
// that text (tools/gen-equipment-boxes.py) into data/<region>/server/.
var equipmentBoxes map[uint32][][2]uint32

func loadEquipmentBoxes() error {
	raw, err := gamedata.ServerFile("equipment_boxes.json")
	if err != nil {
		return err
	}
	equipmentBoxes = map[uint32][][2]uint32{}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &equipmentBoxes); err != nil {
		return fmt.Errorf("server/equipment_boxes.json: %w", err)
	}
	for id, contents := range equipmentBoxes {
		logger.WithFields("Config",
			logger.FieldValue("box", id),
			logger.FieldValue("lines", len(contents)),
		).Debug("equipment box loaded")
	}
	return nil
}

// expandBundles replaces a gift box drop with the equipment it holds, so the
// box never lands in the depot where the client can't open it. Every copy of
// the box gives its whole list.
func expandBundles(drops []*protobuf.DROPINFO) []*protobuf.DROPINFO {
	expanded := make([]*protobuf.DROPINFO, 0, len(drops))
	for _, entry := range drops {
		contents, ok := equipmentBoxes[entry.GetId()]
		if entry.GetType() != dropTypeItem || !ok || len(contents) == 0 {
			expanded = append(expanded, entry)
			continue
		}
		logger.WithFields("Drops",
			logger.FieldValue("box", entry.GetId()),
			logger.FieldValue("count", entry.GetNumber()),
		).Debug("equipment box opened")
		for range entry.GetNumber() {
			for _, item := range contents {
				expanded = append(expanded, dropInfo(dropTypeEquip, item[0], item[1]))
			}
		}
	}
	return expanded
}
