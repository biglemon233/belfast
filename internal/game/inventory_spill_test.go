package game

import "testing"

// TestResourceSpill checks oil stops at its cap and the excess goes into the
// mail storage, up to its capacity, the rest lost.
func TestResourceSpill(t *testing.T) {
	cases := []struct {
		name                              string
		limit, owned, amount              uint32
		storeOwned, storeCapacity         uint32
		wantAdded, wantStored             uint32
	}{
		{"under the cap", 9100, 1000, 500, 0, 600000, 500, 0},
		{"reaching the cap", 9100, 8800, 500, 0, 600000, 300, 200},
		{"at the cap", 9100, 9100, 500, 0, 600000, 0, 500},
		{"storage full too", 9100, 9100, 500, 599800, 600000, 0, 200},
		{"nothing fits", 9100, 9100, 500, 600000, 600000, 0, 0},
		{"no cap", 0, 0, 500, 0, 600000, 500, 0},
	}
	for _, c := range cases {
		added, stored := resourceSpill(c.limit, c.owned, c.amount, c.storeOwned, c.storeCapacity)
		if added != c.wantAdded || stored != c.wantStored {
			t.Errorf("%s: added=%d stored=%d, want %d/%d", c.name, added, stored, c.wantAdded, c.wantStored)
		}
	}
}
