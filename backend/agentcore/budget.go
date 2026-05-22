package agentcore

import (
	"fmt"
	"sync"
)

// ExceededKind distinguishes the type of budget overrun.
type ExceededKind string

const (
	ExceededIterations   ExceededKind = "iterations"
	ExceededCost         ExceededKind = "cost"
	ExceededOutputTokens ExceededKind = "output_tokens"
)

// ExceededError is returned when the budget is exhausted.
type ExceededError struct {
	Kind  ExceededKind
	Limit float64
	Used  float64
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("budget exceeded: %s (limit=%.4g used=%.4g)", e.Kind, e.Limit, e.Used)
}

// IterationBudget limits the number of agent loop iterations, total LLM cost,
// and/or total output tokens across all turns.
// All methods are concurrency-safe.
type IterationBudget struct {
	mu               sync.Mutex
	maxIterations    int // 0 = unlimited
	usedIterations   int
	maxCostUSD       float64 // 0 = unlimited
	usedCostUSD      float64
	maxOutputTokens  int // 0 = unlimited
	usedOutputTokens int
}

// NewIterationBudget creates a budget with iteration and cost limits.
// Pass 0 to disable a limit.
func NewIterationBudget(maxIterations int, maxCostUSD float64) *IterationBudget {
	return &IterationBudget{
		maxIterations: maxIterations,
		maxCostUSD:    maxCostUSD,
	}
}

// NewIterationBudgetFull creates a budget with iteration, cost, and output-token limits.
// Pass 0 to disable an individual limit.
func NewIterationBudgetFull(maxIterations int, maxCostUSD float64, maxOutputTokens int) *IterationBudget {
	return &IterationBudget{
		maxIterations:   maxIterations,
		maxCostUSD:      maxCostUSD,
		maxOutputTokens: maxOutputTokens,
	}
}

// Consume records resource usage.  Returns ExceededError if any limit is hit.
// On error, the usage is NOT applied (check-then-act semantics).
func (b *IterationBudget) Consume(iterations int, costUSD float64) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.maxIterations > 0 && b.usedIterations+iterations > b.maxIterations {
		return &ExceededError{
			Kind:  ExceededIterations,
			Limit: float64(b.maxIterations),
			Used:  float64(b.usedIterations + iterations),
		}
	}
	if b.maxCostUSD > 0 && b.usedCostUSD+costUSD > b.maxCostUSD {
		return &ExceededError{
			Kind:  ExceededCost,
			Limit: b.maxCostUSD,
			Used:  b.usedCostUSD + costUSD,
		}
	}

	b.usedIterations += iterations
	b.usedCostUSD += costUSD
	return nil
}

// Refund returns previously consumed resources (e.g. on tool-block or retry).
// Clamps to zero; never goes negative.
func (b *IterationBudget) Refund(iterations int, costUSD float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.usedIterations -= iterations
	if b.usedIterations < 0 {
		b.usedIterations = 0
	}
	b.usedCostUSD -= costUSD
	if b.usedCostUSD < 0 {
		b.usedCostUSD = 0
	}
}

// Remaining returns how many iterations and how much cost remain.
// A value of -1 means unlimited (no limit set for that dimension).
func (b *IterationBudget) Remaining() (iterations int, costUSD float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.maxIterations == 0 {
		iterations = -1
	} else {
		iterations = b.maxIterations - b.usedIterations
	}
	if b.maxCostUSD == 0 {
		costUSD = -1
	} else {
		costUSD = b.maxCostUSD - b.usedCostUSD
	}
	return
}

// Used returns current resource consumption.
func (b *IterationBudget) Used() (iterations int, costUSD float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usedIterations, b.usedCostUSD
}

// ConsumeOutputTokens records output token usage for a single LLM turn.
// Returns ExceededError if the output-token limit is exceeded.
// On error, the usage is NOT applied (check-then-act semantics).
func (b *IterationBudget) ConsumeOutputTokens(tokens int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.maxOutputTokens > 0 && b.usedOutputTokens+tokens > b.maxOutputTokens {
		return &ExceededError{
			Kind:  ExceededOutputTokens,
			Limit: float64(b.maxOutputTokens),
			Used:  float64(b.usedOutputTokens + tokens),
		}
	}
	b.usedOutputTokens += tokens
	return nil
}

// UsedOutputTokens returns total output tokens consumed so far.
func (b *IterationBudget) UsedOutputTokens() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usedOutputTokens
}

// RemainingOutputTokens returns how many output tokens remain.
// Returns -1 when no output-token limit is set.
func (b *IterationBudget) RemainingOutputTokens() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.maxOutputTokens == 0 {
		return -1
	}
	return b.maxOutputTokens - b.usedOutputTokens
}

// Clone returns a snapshot copy suitable for sub-agents.
// The clone starts with the same used values and is independent thereafter.
func (b *IterationBudget) Clone() *IterationBudget {
	b.mu.Lock()
	defer b.mu.Unlock()
	return &IterationBudget{
		maxIterations:    b.maxIterations,
		usedIterations:   b.usedIterations,
		maxCostUSD:       b.maxCostUSD,
		usedCostUSD:      b.usedCostUSD,
		maxOutputTokens:  b.maxOutputTokens,
		usedOutputTokens: b.usedOutputTokens,
	}
}
