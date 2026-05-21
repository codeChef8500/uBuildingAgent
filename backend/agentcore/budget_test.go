package agentcore

import (
	"errors"
	"sync"
	"testing"
)

func TestBudget_Consume_Normal(t *testing.T) {
	b := NewIterationBudget(5, 1.0)
	if err := b.Consume(3, 0.3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	iter, cost := b.Used()
	if iter != 3 || cost != 0.3 {
		t.Errorf("used: got (%d, %.2f), want (3, 0.30)", iter, cost)
	}
}

func TestBudget_Consume_IterationExceeded(t *testing.T) {
	b := NewIterationBudget(2, 0)
	if err := b.Consume(1, 0); err != nil {
		t.Fatal(err)
	}
	err := b.Consume(2, 0) // would push to 3 > 2
	if err == nil {
		t.Fatal("expected ExceededError")
	}
	var ee *ExceededError
	if !errors.As(err, &ee) {
		t.Fatalf("wrong error type: %T", err)
	}
	if ee.Kind != ExceededIterations {
		t.Errorf("kind: got %q, want %q", ee.Kind, ExceededIterations)
	}
}

func TestBudget_Consume_CostExceeded(t *testing.T) {
	b := NewIterationBudget(0, 1.0)
	err := b.Consume(0, 1.5)
	var ee *ExceededError
	if !errors.As(err, &ee) || ee.Kind != ExceededCost {
		t.Fatalf("expected ExceededCost, got %v", err)
	}
}

func TestBudget_Refund(t *testing.T) {
	b := NewIterationBudget(5, 0)
	_ = b.Consume(3, 0)
	b.Refund(2, 0)
	iter, _ := b.Used()
	if iter != 1 {
		t.Errorf("after refund: used=%d, want 1", iter)
	}
	// Refund below zero clamps to 0
	b.Refund(10, 0)
	iter, _ = b.Used()
	if iter != 0 {
		t.Errorf("after over-refund: used=%d, want 0", iter)
	}
}

func TestBudget_Unlimited(t *testing.T) {
	b := NewIterationBudget(0, 0) // both unlimited
	for i := 0; i < 1000; i++ {
		if err := b.Consume(1, 0.001); err != nil {
			t.Fatalf("unexpected error at iteration %d: %v", i, err)
		}
	}
}

func TestBudget_Remaining(t *testing.T) {
	b := NewIterationBudget(10, 2.0)
	_ = b.Consume(3, 0.5)
	iter, cost := b.Remaining()
	if iter != 7 {
		t.Errorf("remaining iterations: got %d, want 7", iter)
	}
	if cost != 1.5 {
		t.Errorf("remaining cost: got %.2f, want 1.50", cost)
	}
}

func TestBudget_Clone(t *testing.T) {
	b := NewIterationBudget(10, 5.0)
	_ = b.Consume(3, 1.0)
	clone := b.Clone()

	// Modifying clone must not affect original
	_ = clone.Consume(2, 0.5)
	origIter, _ := b.Used()
	cloneIter, _ := clone.Used()
	if origIter != 3 {
		t.Errorf("original changed: used=%d", origIter)
	}
	if cloneIter != 5 {
		t.Errorf("clone used: got %d, want 5", cloneIter)
	}
}

func TestBudget_ConcurrentSafe(t *testing.T) {
	b := NewIterationBudget(1000, 0)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = b.Consume(1, 0)
		}()
	}
	wg.Wait()
	iter, _ := b.Used()
	if iter != 50 {
		t.Errorf("concurrent used: got %d, want 50", iter)
	}
}
