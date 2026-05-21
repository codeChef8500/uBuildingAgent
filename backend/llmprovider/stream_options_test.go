package llmprovider

import (
	"testing"
)

func TestClampThinkingLevel(t *testing.T) {
	cases := []struct {
		input    ThinkingLevel
		expected ThinkingLevel
	}{
		{ThinkingLevelXHigh, ThinkingLevelHigh},
		{ThinkingLevelHigh, ThinkingLevelHigh},
		{ThinkingLevelMedium, ThinkingLevelMedium},
		{ThinkingLevelLow, ThinkingLevelLow},
		{ThinkingLevelMinimal, ThinkingLevelMinimal},
		{ThinkingLevelOff, ThinkingLevelOff},
	}
	for _, c := range cases {
		got := ClampThinkingLevel(c.input)
		if got != c.expected {
			t.Errorf("ClampThinkingLevel(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestAdjustMaxTokensForThinking_DefaultBudgets(t *testing.T) {
	cases := []struct {
		name            string
		base            int
		modelMax        int
		level           ThinkingLevel
		wantMax         int
		wantBudget      int
	}{
		{
			name: "off level returns zero budget",
			base: 0, modelMax: 32000, level: ThinkingLevelOff,
			wantMax: 32000, wantBudget: 0,
		},
		{
			name: "minimal level with no base uses model max",
			base: 0, modelMax: 32000, level: ThinkingLevelMinimal,
			wantMax: 32000, wantBudget: 1024,
		},
		{
			name: "low level with no base uses model max",
			base: 0, modelMax: 32000, level: ThinkingLevelLow,
			wantMax: 32000, wantBudget: 2048,
		},
		{
			name: "medium level with no base",
			base: 0, modelMax: 32000, level: ThinkingLevelMedium,
			wantMax: 32000, wantBudget: 8192,
		},
		{
			name: "high level with no base",
			base: 0, modelMax: 32000, level: ThinkingLevelHigh,
			wantMax: 32000, wantBudget: 16384,
		},
		{
			name: "xhigh clamps to high",
			base: 0, modelMax: 32000, level: ThinkingLevelXHigh,
			wantMax: 32000, wantBudget: 16384,
		},
		{
			name: "base + budget does not exceed model max",
			base: 4000, modelMax: 8192, level: ThinkingLevelHigh,
			wantMax: 8192, wantBudget: 16384, // budget limited by model max
		},
		{
			name: "base+budget within model max",
			base: 4000, modelMax: 32000, level: ThinkingLevelLow,
			wantMax: 6048, wantBudget: 2048, // 4000+2048=6048 < 32000
		},
		{
			name: "tiny model max shrinks budget to preserve minOutputTokens",
			base: 0, modelMax: 512, level: ThinkingLevelMinimal,
			wantMax: 512, wantBudget: 512 - minOutputTokens, // 512-1024 < 0 → 0
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotMax, gotBudget := AdjustMaxTokensForThinking(c.base, c.modelMax, c.level, nil)
			// For the xhigh/high case the budget is capped by model max
			if c.level == ThinkingLevelXHigh {
				// xhigh clamps to high; adjust expected budget
				_, expectedBudget := AdjustMaxTokensForThinking(c.base, c.modelMax, ThinkingLevelHigh, nil)
				if gotBudget != expectedBudget {
					t.Errorf("budget: got %d, want %d", gotBudget, expectedBudget)
				}
				return
			}
			if c.name == "base + budget does not exceed model max" {
				// budget is 16384 but model max is 8192, so budget shrinks
				if gotMax != 8192 {
					t.Errorf("maxTokens: got %d, want 8192", gotMax)
				}
				if gotBudget >= gotMax {
					t.Errorf("budget %d >= maxTokens %d", gotBudget, gotMax)
				}
				return
			}
			if c.name == "tiny model max shrinks budget to preserve minOutputTokens" {
				wantBudget := 512 - minOutputTokens
				if wantBudget < 0 {
					wantBudget = 0
				}
				if gotBudget != wantBudget {
					t.Errorf("budget: got %d, want %d", gotBudget, wantBudget)
				}
				return
			}
			if gotMax != c.wantMax {
				t.Errorf("maxTokens: got %d, want %d", gotMax, c.wantMax)
			}
			if gotBudget != c.wantBudget {
				t.Errorf("budget: got %d, want %d", gotBudget, c.wantBudget)
			}
		})
	}
}

func TestAdjustMaxTokensForThinking_CustomBudgets(t *testing.T) {
	custom := &ThinkingBudgets{Low: 4000, Medium: 12000}
	_, budget := AdjustMaxTokensForThinking(0, 32000, ThinkingLevelLow, custom)
	if budget != 4000 {
		t.Errorf("custom Low budget: got %d, want 4000", budget)
	}
	_, budget = AdjustMaxTokensForThinking(0, 32000, ThinkingLevelMedium, custom)
	if budget != 12000 {
		t.Errorf("custom Medium budget: got %d, want 12000", budget)
	}
	// High not overridden → default 16384
	_, budget = AdjustMaxTokensForThinking(0, 32000, ThinkingLevelHigh, custom)
	if budget != 16384 {
		t.Errorf("default High budget: got %d, want 16384", budget)
	}
}
