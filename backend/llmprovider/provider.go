package llmprovider

import "context"

// ApiProvider — LLM API 协议实现接口
//
// 每个实现对应一种 API 协议（如 openai-completions、anthropic-messages），
// 注册到 Registry 后通过 Model.Api 字段路由。
//
// 合约（两个方法均须遵守）：
//   - 永不 panic；错误必须编码为 StreamEvent{Type: StreamEventError, Err: err} 发送到 channel
//   - channel 在所有事件（含 error）发送后必须 close
//   - 调用方 ctx 取消后，应尽快停止并 close channel
type ApiProvider interface {
	// ApiType 返回该实现对应的 API 协议类型（用于 Registry 路由）
	ApiType() ApiType

	// Stream — 低层入口，接受完整 StreamOptions，provider 完全掌控请求细节
	// 返回只读 channel，close 表示流结束
	Stream(ctx context.Context, model Model, conv Context, opts StreamOptions) <-chan StreamEvent

	// StreamSimple — 高层入口，接受 SimpleStreamOptions
	// provider 内部调用 AdjustMaxTokensForThinking 完成 ThinkingLevel → budget 翻译
	// 返回只读 channel，close 表示流结束
	StreamSimple(ctx context.Context, model Model, conv Context, opts SimpleStreamOptions) <-chan StreamEvent
}

// StreamFn — 供 agentcore 使用的流式调用函数类型
// 封装 ApiProvider.StreamSimple，隐藏具体 Provider 实现
type StreamFn func(
	ctx  context.Context,
	model Model,
	conv Context,
	opts SimpleStreamOptions,
) <-chan StreamEvent

// WrapProvider 将 ApiProvider 包装为 StreamFn（方便 agentcore 注入）
func WrapProvider(p ApiProvider) StreamFn {
	return func(ctx context.Context, model Model, conv Context, opts SimpleStreamOptions) <-chan StreamEvent {
		return p.StreamSimple(ctx, model, conv, opts)
	}
}

// ErrorChan 创建一个立即发送 error 事件后关闭的 channel（工具函数）
func ErrorChan(err error) <-chan StreamEvent {
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: StreamEventError, Err: err}
	close(ch)
	return ch
}
