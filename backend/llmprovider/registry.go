package llmprovider

import (
	"context"
	"fmt"
	"sync"
)

// Registry — ApiProvider 注册表
//
// 以 ApiType 为主键（路由键），与 provider 公司名解耦。
// sourceId 用于插件/模块级批量注销（如插件卸载时清除其注册的所有 provider）。
type Registry struct {
	mu        sync.RWMutex
	providers map[ApiType]ApiProvider
	bySource  map[string][]ApiType // sourceId → []ApiType，用于批量 Unregister
}

// NewRegistry 创建一个空的 Registry
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[ApiType]ApiProvider),
		bySource:  make(map[string][]ApiType),
	}
}

// Register 注册一个 ApiProvider
//
// sourceId 可选（空字符串 = 无来源标记）；同一 ApiType 重复注册会覆盖旧实现。
func (r *Registry) Register(p ApiProvider, sourceId string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	api := p.ApiType()
	r.providers[api] = p

	if sourceId != "" {
		r.bySource[sourceId] = append(r.bySource[sourceId], api)
	}
}

// Unregister 按 sourceId 批量注销所有该来源注册的 ApiProvider
func (r *Registry) Unregister(sourceId string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, api := range r.bySource[sourceId] {
		delete(r.providers, api)
	}
	delete(r.bySource, sourceId)
}

// Get 按 ApiType 查找 ApiProvider
func (r *Registry) Get(api ApiType) (ApiProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[api]
	return p, ok
}

// Resolve 按 Model.Api 路由到对应 ApiProvider
func (r *Registry) Resolve(model Model) (ApiProvider, bool) {
	return r.Get(model.Api)
}

// MustResolve 按 Model.Api 路由，找不到时 panic（用于测试/初始化断言）
func (r *Registry) MustResolve(model Model) ApiProvider {
	p, ok := r.Resolve(model)
	if !ok {
		panic(fmt.Sprintf("llmprovider: no ApiProvider registered for api %q (model %q)", model.Api, model.ID))
	}
	return p
}

// List 返回所有已注册的 ApiProvider（顺序不保证）
func (r *Registry) List() []ApiProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ApiProvider, 0, len(r.providers))
	for _, p := range r.providers {
		result = append(result, p)
	}
	return result
}

// Clear 清除所有注册的 ApiProvider
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.providers = make(map[ApiType]ApiProvider)
	r.bySource = make(map[string][]ApiType)
}

// ── 全局默认 Registry ──

// DefaultRegistry — 进程级默认注册表
var DefaultRegistry = NewRegistry()

// Register 向 DefaultRegistry 注册 ApiProvider
func Register(p ApiProvider, sourceId string) {
	DefaultRegistry.Register(p, sourceId)
}

// Unregister 从 DefaultRegistry 按 sourceId 批量注销
func Unregister(sourceId string) {
	DefaultRegistry.Unregister(sourceId)
}

// Get 从 DefaultRegistry 按 ApiType 查找
func Get(api ApiType) (ApiProvider, bool) {
	return DefaultRegistry.Get(api)
}

// Resolve 从 DefaultRegistry 按 Model.Api 路由
func Resolve(model Model) (ApiProvider, bool) {
	return DefaultRegistry.Resolve(model)
}

// Stream 通过 DefaultRegistry 路由后调用 ApiProvider.Stream
// 找不到 provider 时返回 error channel
func Stream(ctx context.Context, model Model, conv Context, opts StreamOptions) <-chan StreamEvent {
	p, ok := DefaultRegistry.Resolve(model)
	if !ok {
		return ErrorChan(fmt.Errorf("llmprovider: no provider registered for api %q", model.Api))
	}
	return p.Stream(ctx, model, conv, opts)
}

// StreamSimple 通过 DefaultRegistry 路由后调用 ApiProvider.StreamSimple
func StreamSimple(ctx context.Context, model Model, conv Context, opts SimpleStreamOptions) <-chan StreamEvent {
	p, ok := DefaultRegistry.Resolve(model)
	if !ok {
		return ErrorChan(fmt.Errorf("llmprovider: no provider registered for api %q", model.Api))
	}
	return p.StreamSimple(ctx, model, conv, opts)
}
