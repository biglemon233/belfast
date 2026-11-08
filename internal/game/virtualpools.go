package game

import (
	"regexp"
	"strconv"
	"strings"

	"cmp"
	"slices"
)

// placeholderTier is the "T<n>" a virtual placeholder's name ends with
// ("未知教材T4", "未知科技箱T2"): the tier of what it can turn into.
var placeholderTier = regexp.MustCompile(`T([0-9]+)$`)

// deriveVirtualPools fills the contents the client data leaves out: the
// "unknown" items (item_virtual_data_statistics) are random boxes, most with
// a display_icon pool, but the tech box and equipment blueprint ones
// (54031-54035, 54039, 54049-54051) come with none. They name a tier, so:
//   - a family that has pooled members (the tech boxes' 54021-54025) gives
//     that tier's entries;
//   - otherwise the tier's equipment blueprints (item_data_statistics type 9)
//     are the pool, as the "unknown equipment blueprint" ones name.
//
// The official server keeps these pools, the client tables only hint at them:
// this is the closest the data allows.
func deriveVirtualPools(virtuals map[uint32]virtualItemConfig, items map[uint32]itemNameType) {
	for id, row := range virtuals {
		if len(dropPools[id]) > 0 {
			continue
		}
		match := placeholderTier.FindStringSubmatch(row.Name)
		if match == nil {
			continue
		}
		tier, err := strconv.Atoi(match[1])
		if err != nil || tier <= 0 {
			continue
		}
		prefix := row.Name[:len(row.Name)-len(match[0])]
		var pool []dropRef
		seen := map[dropRef]bool{}
		for siblingID, sibling := range virtuals {
			if siblingID == id || len(dropPools[siblingID]) == 0 || !strings.HasPrefix(sibling.Name, prefix) {
				continue
			}
			for _, ref := range dropPools[siblingID] {
				// the pooled families number their tiers as the last digit
				if int(ref.id%10) != tier || seen[ref] {
					continue
				}
				seen[ref] = true
				pool = append(pool, ref)
			}
		}
		if len(pool) == 0 {
			marker := "T" + strconv.Itoa(tier)
			for itemID, item := range items {
				if item.Type == itemTypeDesign && strings.Contains(item.Display, marker) && !seen[dropRef{kind: dropTypeItem, id: itemID}] {
					seen[dropRef{kind: dropTypeItem, id: itemID}] = true
					pool = append(pool, dropRef{kind: dropTypeItem, id: itemID})
				}
			}
		}
		slices.SortFunc(pool, func(a, b dropRef) int {
			if a.kind != b.kind {
				return cmp.Compare(a.kind, b.kind)
			}
			return cmp.Compare(a.id, b.id)
		})
		if len(pool) > 0 {
			dropPools[id] = pool
		}
	}
}
