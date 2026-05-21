package openaicompat

import (
	"testing"

	"github.com/ubuildingagent/backend/llmprovider"
)

func TestKnownProfiles_OpenAI(t *testing.T) {
	p := KnownProfiles("https://api.openai.com/v1")
	if p == nil {
		t.Fatal("expected non-nil profile for OpenAI")
	}
	if !llmprovider.BoolVal(p.SupportsDeveloperRole, false) {
		t.Error("OpenAI should support developer role")
	}
	if p.ThinkingFormat != llmprovider.ThinkingFormatOpenAI {
		t.Errorf("ThinkingFormat: got %q, want %q", p.ThinkingFormat, llmprovider.ThinkingFormatOpenAI)
	}
}

func TestKnownProfiles_DeepSeek(t *testing.T) {
	p := KnownProfiles("https://api.deepseek.com/v1")
	if p == nil {
		t.Fatal("expected non-nil profile for DeepSeek")
	}
	if p.ThinkingFormat != llmprovider.ThinkingFormatDeepSeek {
		t.Errorf("ThinkingFormat: got %q, want %q", p.ThinkingFormat, llmprovider.ThinkingFormatDeepSeek)
	}
}

func TestKnownProfiles_VolcengineArk(t *testing.T) {
	p := KnownProfiles("https://ark.cn-beijing.volces.com/api/coding/v3")
	if p == nil {
		t.Fatal("expected non-nil profile for Volcengine Ark")
	}
	if llmprovider.BoolVal(p.SupportsStore, true) {
		t.Error("Volcengine Ark should not support store")
	}
}

func TestKnownProfiles_Unknown(t *testing.T) {
	p := KnownProfiles("https://mylocal.example.com/v1")
	if p != nil {
		t.Error("expected nil for unknown URL")
	}
}

func TestMergeCompat_OverrideWins(t *testing.T) {
	base := &llmprovider.OpenAICompletionsCompat{
		ThinkingFormat:  llmprovider.ThinkingFormatOpenAI,
		SupportsStore:   llmprovider.BoolPtr(true),
	}
	override := &llmprovider.OpenAICompletionsCompat{
		ThinkingFormat: llmprovider.ThinkingFormatDeepSeek,
	}
	merged := MergeCompat(base, override)
	if merged.ThinkingFormat != llmprovider.ThinkingFormatDeepSeek {
		t.Errorf("expected override to win, got %q", merged.ThinkingFormat)
	}
	// base field preserved when override is zero-value
	if !llmprovider.BoolVal(merged.SupportsStore, false) {
		t.Error("SupportsStore should be preserved from base")
	}
}

func TestMergeCompat_NilOverride(t *testing.T) {
	base := &llmprovider.OpenAICompletionsCompat{ThinkingFormat: llmprovider.ThinkingFormatQwen}
	merged := MergeCompat(base, nil)
	if merged.ThinkingFormat != llmprovider.ThinkingFormatQwen {
		t.Error("nil override should return base unchanged")
	}
}

func TestMergeCompat_NilBase(t *testing.T) {
	override := &llmprovider.OpenAICompletionsCompat{ThinkingFormat: llmprovider.ThinkingFormatDeepSeek}
	merged := MergeCompat(nil, override)
	if merged.ThinkingFormat != llmprovider.ThinkingFormatDeepSeek {
		t.Error("nil base should return override")
	}
}
