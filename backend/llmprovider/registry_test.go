package llmprovider

import (
	"context"
	"sync"
	"testing"
)

// mockProvider — 用于测试的 ApiProvider 实现
type mockProvider struct {
	apiType ApiType
	calls   int
	mu      sync.Mutex
}

func (m *mockProvider) ApiType() ApiType { return m.apiType }

func (m *mockProvider) Stream(_ context.Context, _ Model, _ Context, _ StreamOptions) <-chan StreamEvent {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	ch := make(chan StreamEvent, 1)
	close(ch)
	return ch
}

func (m *mockProvider) StreamSimple(_ context.Context, _ Model, _ Context, _ SimpleStreamOptions) <-chan StreamEvent {
	return m.Stream(context.Background(), Model{}, Context{}, StreamOptions{})
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	p := &mockProvider{apiType: ApiOpenAICompletions}
	r.Register(p, "")

	got, ok := r.Get(ApiOpenAICompletions)
	if !ok {
		t.Fatal("expected to find registered provider")
	}
	if got.ApiType() != ApiOpenAICompletions {
		t.Errorf("got api type %q, want %q", got.ApiType(), ApiOpenAICompletions)
	}

	_, ok = r.Get(ApiAnthropicMessages)
	if ok {
		t.Fatal("expected missing provider to return false")
	}
}

func TestRegistry_Resolve(t *testing.T) {
	r := NewRegistry()
	p := &mockProvider{apiType: ApiAnthropicMessages}
	r.Register(p, "source-a")

	model := Model{ID: "claude-3", Api: ApiAnthropicMessages}
	got, ok := r.Resolve(model)
	if !ok || got.ApiType() != ApiAnthropicMessages {
		t.Fatal("Resolve failed")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	p1 := &mockProvider{apiType: ApiOpenAICompletions}
	p2 := &mockProvider{apiType: ApiAnthropicMessages}
	r.Register(p1, "plugin-x")
	r.Register(p2, "plugin-x")

	r.Unregister("plugin-x")

	_, ok1 := r.Get(ApiOpenAICompletions)
	_, ok2 := r.Get(ApiAnthropicMessages)
	if ok1 || ok2 {
		t.Fatal("expected providers to be unregistered after Unregister(sourceId)")
	}
}

func TestRegistry_OverwriteSameApiType(t *testing.T) {
	r := NewRegistry()
	p1 := &mockProvider{apiType: ApiOpenAICompletions}
	p2 := &mockProvider{apiType: ApiOpenAICompletions}
	r.Register(p1, "")
	r.Register(p2, "")

	got, _ := r.Get(ApiOpenAICompletions)
	if got != p2 {
		t.Error("second Register should overwrite first")
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := &mockProvider{apiType: ApiGoogleGenerativeAI}
			r.Register(p, "")
			r.Get(ApiGoogleGenerativeAI)
			r.List()
		}()
	}
	wg.Wait()
}
