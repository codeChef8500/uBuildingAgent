package agentcore

import (
	"testing"
)

func TestToolRegistry_RegisterAndGet(t *testing.T) {
	r := NewToolRegistry()
	tool := AgentTool{Name: "echo", Description: "echo tool"}
	r.Register(tool, nil)

	got, ok := r.Get("echo")
	if !ok {
		t.Fatal("expected to find registered tool")
	}
	if got.Name != "echo" {
		t.Errorf("name: got %q", got.Name)
	}
}

func TestToolRegistry_Unregister(t *testing.T) {
	r := NewToolRegistry()
	r.Register(AgentTool{Name: "tmp"}, nil)
	r.Unregister("tmp")
	_, ok := r.Get("tmp")
	if ok {
		t.Error("expected tool to be unregistered")
	}
}

func TestToolRegistry_TTLExpired(t *testing.T) {
	r := NewToolRegistry()
	expired := false
	r.Register(AgentTool{Name: "ttl_tool"}, func() bool { return !expired })

	_, ok := r.Get("ttl_tool")
	if !ok {
		t.Fatal("expected tool available before expiry")
	}

	expired = true
	_, ok = r.Get("ttl_tool")
	if ok {
		t.Error("expected tool hidden after TTL expiry")
	}
}

func TestToolRegistry_ListEnabled_FiltersExpired(t *testing.T) {
	r := NewToolRegistry()
	alive := true
	r.Register(AgentTool{Name: "alive"}, func() bool { return alive })
	r.Register(AgentTool{Name: "dead"}, func() bool { return false })
	r.Register(AgentTool{Name: "no_ttl"}, nil)

	list := r.ListEnabled()
	names := map[string]bool{}
	for _, t := range list {
		names[t.Name] = true
	}

	if !names["alive"] {
		t.Error("expected 'alive' in list")
	}
	if names["dead"] {
		t.Error("expected 'dead' NOT in list")
	}
	if !names["no_ttl"] {
		t.Error("expected 'no_ttl' in list")
	}
}

func TestToolRegistry_Overwrite(t *testing.T) {
	r := NewToolRegistry()
	r.Register(AgentTool{Name: "tool", Description: "v1"}, nil)
	r.Register(AgentTool{Name: "tool", Description: "v2"}, nil)

	got, _ := r.Get("tool")
	if got.Description != "v2" {
		t.Errorf("expected v2 after overwrite, got %q", got.Description)
	}
}
