package agentcore

import "sync"

// registeredTool wraps an AgentTool with an optional TTL check function.
type registeredTool struct {
	tool     AgentTool
	ttlCheck func() bool // nil = always enabled; returns false = disabled/expired
}

// ToolRegistry manages a named set of AgentTools.
// It is concurrency-safe.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*registeredTool
}

// NewToolRegistry creates an empty ToolRegistry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]*registeredTool)}
}

// Register adds or replaces a tool.
// ttlCheck is optional; if non-nil it is called by ListEnabled to filter expired tools.
func (r *ToolRegistry) Register(tool AgentTool, ttlCheck func() bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name] = &registeredTool{tool: tool, ttlCheck: ttlCheck}
}

// Unregister removes a tool by name.  No-op if not found.
func (r *ToolRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// Get returns a registered tool by name (nil, false if not found or TTL expired).
func (r *ToolRegistry) Get(name string) (*AgentTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.tools[name]
	if !ok {
		return nil, false
	}
	if rt.ttlCheck != nil && !rt.ttlCheck() {
		return nil, false // TTL expired
	}
	cp := rt.tool
	return &cp, true
}

// ListEnabled returns all tools whose TTL check passes (or have no TTL check).
// Order is not guaranteed.
func (r *ToolRegistry) ListEnabled() []AgentTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AgentTool, 0, len(r.tools))
	for _, rt := range r.tools {
		if rt.ttlCheck != nil && !rt.ttlCheck() {
			continue
		}
		out = append(out, rt.tool)
	}
	return out
}

// Len returns the total number of registered tools (including expired).
func (r *ToolRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}
