// Package envconfig parses a KEY=VALUE .env file and maps LLM_* variables
// into a strongly-typed LLMConfig.  No external dependencies required.
package envconfig

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/ubuildingagent/backend/llmprovider"
)

// LLMConfig holds LLM provider settings read from the .env file.
type LLMConfig struct {
	Type    string // LLM_TYPE  — "openai" | "anthropic" | "google" | "qwen"
	Model   string // LLM_MODEL_NAME
	APIKey  string // LLM_API_KEY
	BaseURL string // LLM_BASE_URL
}

// VLMConfig holds Vision-LLM provider settings (VLM_* keys).
type VLMConfig struct {
	Type             string // VLM_TYPE
	Model            string // VLM_MODEL_NAME
	APIKey           string // VLM_API_KEY
	BaseURL          string // VLM_BASE_URL
	DetectorEndpoint string // DETECTOR_ENDPOINT — Python Detector Sidecar URL
}

// LoadFromFile parses a .env file and returns an LLMConfig.
// Lines starting with '#' or empty lines are ignored.
// Returns an error only for I/O failures; missing keys result in empty fields.
func LoadFromFile(path string) (*LLMConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("envconfig: open %q: %w", path, err)
	}
	defer f.Close()

	kv := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip optional surrounding quotes
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') ||
			(val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		kv[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("envconfig: scan %q: %w", path, err)
	}

	return &LLMConfig{
		Type:    kv["LLM_TYPE"],
		Model:   kv["LLM_MODEL_NAME"],
		APIKey:  kv["LLM_API_KEY"],
		BaseURL: kv["LLM_BASE_URL"],
	}, nil
}

// LoadVLMFromFile parses a .env file and returns a VLMConfig.
// Returns nil (no error) when VLM keys are absent — VLM is optional.
func LoadVLMFromFile(path string) (*VLMConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("envconfig: open %q: %w", path, err)
	}
	defer f.Close()

	kv := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') ||
			(val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		kv[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("envconfig: scan %q: %w", path, err)
	}

	return &VLMConfig{
		Type:             kv["VLM_TYPE"],
		Model:            kv["VLM_MODEL_NAME"],
		APIKey:           kv["VLM_API_KEY"],
		BaseURL:          kv["VLM_BASE_URL"],
		DetectorEndpoint: kv["DETECTOR_ENDPOINT"],
	}, nil
}

// Validate reports an error if any required field is empty.
func (c *LLMConfig) Validate() error {
	if c.Model == "" {
		return fmt.Errorf("envconfig: LLM_MODEL_NAME is required")
	}
	if c.APIKey == "" {
		return fmt.Errorf("envconfig: LLM_API_KEY is required")
	}
	if c.BaseURL == "" {
		return fmt.Errorf("envconfig: LLM_BASE_URL is required")
	}
	return nil
}

// ToModel converts LLMConfig into an llmprovider.Model ready for streaming.
// LLM_TYPE is mapped to ApiType:
//
//	"openai"    → ApiOpenAICompletions  (also used for Volcengine Ark, MiniMax, etc.)
//	"anthropic" → ApiAnthropicMessages
//	"google"    → ApiGoogleGenerativeAI
//	"qwen"      → ApiDashScopeMessages
//	""  / other → ApiOpenAICompletions  (default: assume OpenAI-compat)
func (c *LLMConfig) ToModel() llmprovider.Model {
	api := llmprovider.ApiOpenAICompletions
	switch strings.ToLower(c.Type) {
	case "anthropic":
		api = llmprovider.ApiAnthropicMessages
	case "google":
		api = llmprovider.ApiGoogleGenerativeAI
	case "qwen":
		api = llmprovider.ApiDashScopeMessages
	}

	return llmprovider.Model{
		ID:            c.Model,
		Api:           api,
		Provider:      c.Type,
		BaseURL:       c.BaseURL,
		MaxOutput:     4096,
		SupportsTools: true,
	}
}

// ToStreamOptions builds StreamOptions from this config.
func (c *LLMConfig) ToStreamOptions() llmprovider.StreamOptions {
	return llmprovider.StreamOptions{
		APIKey: c.APIKey,
	}
}

// IsConfigured reports whether all required VLM fields are present.
func (v *VLMConfig) IsConfigured() bool {
	return v != nil && v.Model != "" && v.APIKey != "" && v.BaseURL != ""
}

// ToModel converts VLMConfig to an llmprovider.Model.
func (v *VLMConfig) ToModel() llmprovider.Model {
	return llmprovider.Model{
		ID:            v.Model,
		Api:           llmprovider.ApiOpenAICompletions, // VLM always openai-compat
		Provider:      v.Type,
		BaseURL:       v.BaseURL,
		MaxOutput:     1024,
		SupportsTools: false,
	}
}
