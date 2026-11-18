package main

import (
	"testing"
)

func TestDailyNarrativeRejectsUnverifiedNumbers(t *testing.T) {
	for _, text := range []string{"Revenue was 82,414,100", "Sales reached two million rupiah", "There were 119 sales"} {
		if !generatedNumber.MatchString(text) {
			t.Fatalf("did not reject %q", text)
		}
	}
	if generatedNumber.MatchString("Sales and receipts were recorded; some items need replenishment.") {
		t.Fatal("rejected qualitative summary")
	}
}

func TestCalculateRestock(t *testing.T) {
	tests := []struct {
		name                 string
		item                 restockItem
		projected, suggested float64
		limited              bool
	}{
		{"demand target", restockItem{OnHand: 2, Reorder: 5, Incoming: 1, Pending: 1, Usage: 20}, 4, 16, false},
		{"incoming avoids duplicate", restockItem{OnHand: 2, Reorder: 5, Incoming: 4, Usage: 20}, 6, 0, false},
		{"minimum target with no usage", restockItem{OnHand: 3, Reorder: 5}, 3, 7, true},
		{"round up", restockItem{OnHand: 1.5, Reorder: 2, Usage: 4.1}, 1.5, 3, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateRestock(tt.item)
			if got.Projected != tt.projected || got.Suggested != tt.suggested || got.LowHistory != tt.limited {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
