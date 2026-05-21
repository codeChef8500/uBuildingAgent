package providers

import (
	"context"
	"fmt"
	"sync"

	"github.com/ubuildingagent/backend/llmprovider"
)

// LazyProvider — 延迟初始化的 ApiProvider 包装器
//
// 对应 Pi packages/ai/src/providers/register-builtins.ts createLazyStream 模式。
//
// 首次调用 Stream 或 StreamSimple 时触发 initFn，初始化失败不会 panic，
// 而是编码为 StreamEvent{Type: StreamEventError} 发送到 channel。
// sync.Once 保证 initFn 只执行一次（幂等）。
type LazyProvider struct {
	apiType llmprovider.ApiType
	once    sync.Once
	inner   llmprovider.ApiProvider
	initErr error
	initFn  func() (llmprovider.ApiProvider, error)
}

// NewLazyProvider 创建一个延迟初始化的 LazyProvider
func NewLazyProvider(
	api llmprovider.ApiType,
	initFn func() (llmprovider.ApiProvider, error),
) *LazyProvider {
	return &LazyProvider{
		apiType: api,
		initFn:  initFn,
	}
}

// ApiType 实现 llmprovider.ApiProvider 接口
func (l *LazyProvider) ApiType() llmprovider.ApiType {
	return l.apiType
}

// init 执行一次性初始化（sync.Once 保护）
func (l *LazyProvider) init() {
	l.once.Do(func() {
		l.inner, l.initErr = l.initFn()
	})
}

// Stream 实现 llmprovider.ApiProvider 接口（延迟加载版）
func (l *LazyProvider) Stream(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.StreamOptions,
) <-chan llmprovider.StreamEvent {
	l.init()
	if l.initErr != nil {
		return llmprovider.ErrorChan(fmt.Errorf("provider %q init failed: %w", l.apiType, l.initErr))
	}
	return l.inner.Stream(ctx, model, conv, opts)
}

// StreamSimple 实现 llmprovider.ApiProvider 接口（延迟加载版）
func (l *LazyProvider) StreamSimple(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.SimpleStreamOptions,
) <-chan llmprovider.StreamEvent {
	l.init()
	if l.initErr != nil {
		return llmprovider.ErrorChan(fmt.Errorf("provider %q init failed: %w", l.apiType, l.initErr))
	}
	return l.inner.StreamSimple(ctx, model, conv, opts)
}
