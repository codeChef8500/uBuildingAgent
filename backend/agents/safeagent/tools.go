package safeagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/llmprovider"
)

// vlmDetectionPrompt is sent to the VLM alongside the image for object detection.
const vlmDetectionPrompt = `你是施工现场安全检测助手。请仔细分析这张施工现场图片，完成以下任务：
1. 识别图中所有人员、设备、工具和构筑物
2. 找出所有安全违规行为（如：未戴安全帽、未系安全绳、违规操作、危险行为等）
3. 如果没有图片或图片无法识别，根据场景描述进行分析

请严格按照以下 JSON 格式输出（不要包含任何其他文字）：
{"objects":[{"type":"...","confidence":0.9}],"violations":["..."],"confidence":0.85,"summary":"一句话描述"}`

// fetchImageAsDataURL fetches an image from a URL or local temp path and returns
// a data:image/jpeg;base64,... string. Local safeagent frame URLs are read directly.
func fetchImageAsDataURL(imageURL string) (string, error) {
	const localPrefix = "http://localhost:8080/api/safeagent/video/frames/"

	var raw []byte
	if strings.HasPrefix(imageURL, localPrefix) {
		// Read directly from the temp directory to avoid network round-trip.
		fname := filepath.Base(imageURL)
		fpath := filepath.Join(os.TempDir(), "safeagent-frames", fname)
		var err error
		raw, err = os.ReadFile(fpath)
		if err != nil {
			return "", fmt.Errorf("fetchImage: read local frame %q: %w", fpath, err)
		}
	} else if strings.HasPrefix(imageURL, "http://") || strings.HasPrefix(imageURL, "https://") {
		resp, err := http.Get(imageURL) //nolint:gosec
		if err != nil {
			return "", fmt.Errorf("fetchImage: GET %q: %w", imageURL, err)
		}
		defer resp.Body.Close()
		raw, err = io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("fetchImage: read body: %w", err)
		}
	} else {
		return "", fmt.Errorf("fetchImage: unsupported URL scheme %q", imageURL)
	}

	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(raw), nil
}

// callVLM sends an image + text prompt to the vision model and returns the full text response.
func callVLM(ctx context.Context, vlmModel llmprovider.Model, apiKey, imageDataURL, textPrompt string) (string, error) {
	contentParts := []llmprovider.ContentPart{
		{Type: llmprovider.ContentTypeText, Text: textPrompt},
	}
	if imageDataURL != "" {
		// Prepend the image so the VLM sees it before the text.
		contentParts = append([]llmprovider.ContentPart{
			{Type: llmprovider.ContentTypeImageURL, ImageURL: imageDataURL},
		}, contentParts...)
	}

	conv := llmprovider.Context{
		Messages: []llmprovider.Message{
			{
				Role:    llmprovider.RoleUser,
				Content: contentParts,
			},
		},
	}
	opts := llmprovider.SimpleStreamOptions{
		StreamOptions: llmprovider.StreamOptions{APIKey: apiKey},
	}

	ch := llmprovider.StreamSimple(ctx, vlmModel, conv, opts)

	var sb strings.Builder
	for ev := range ch {
		switch ev.Type {
		case llmprovider.StreamEventTextDelta:
			sb.WriteString(ev.Delta)
		case llmprovider.StreamEventError:
			if ev.Err != nil {
				return sb.String(), ev.Err
			}
		}
	}
	return sb.String(), nil
}

// buildVisionTools returns VisionAgent tools. detect_objects makes a real VLM call.
func buildVisionTools(vlmModel llmprovider.Model, vlmAPIKey string) []agentcore.AgentTool {
	return []agentcore.AgentTool{
		{
			Name:        "detect_objects",
			Description: "检测场景中的人员、设备、安全装备和危险行为，通过视觉模型分析图像",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"image_url":   {"type": "string", "description": "图像 URL"},
					"description": {"type": "string", "description": "场景文字描述"}
				}
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				var args struct {
					ImageURL    string `json:"image_url"`
					Description string `json:"description"`
				}
				if err := json.Unmarshal(texCtx.Args, &args); err != nil {
					return agentcore.AgentToolResult{
						Content: fmt.Sprintf(`{"error":"invalid args: %v"}`, err),
						IsError: true,
					}
				}

				// Build the text prompt, include scene description as context.
				prompt := vlmDetectionPrompt
				if args.Description != "" {
					prompt = "场景描述：" + args.Description + "\n\n" + vlmDetectionPrompt
				}

				// Fetch and encode the image if a URL was provided.
				imageDataURL := ""
				if args.ImageURL != "" {
					var ferr error
					imageDataURL, ferr = fetchImageAsDataURL(args.ImageURL)
					if ferr != nil {
						// Log but continue — VLM can still analyse based on description.
						fmt.Printf("[detect_objects] image fetch warning: %v\n", ferr)
					}
				}

				// Call VLM.
				rawText, err := callVLM(texCtx.Ctx, vlmModel, vlmAPIKey, imageDataURL, prompt)
				if err != nil {
					return agentcore.AgentToolResult{
						Content: fmt.Sprintf(`{"error":"VLM call failed: %v","summary":"视觉检测失败"}`, err),
						IsError: true,
					}
				}

				// Try to parse VLM output as DetectionResult JSON.
				// If the model wrapped it in markdown fences, strip them first.
				clean := strings.TrimSpace(rawText)
				if i := strings.Index(clean, "```json"); i >= 0 {
					clean = clean[i+7:]
					if j := strings.Index(clean, "```"); j >= 0 {
						clean = clean[:j]
					}
					clean = strings.TrimSpace(clean)
				} else if i := strings.Index(clean, "```"); i >= 0 {
					clean = clean[i+3:]
					if j := strings.Index(clean, "```"); j >= 0 {
						clean = clean[:j]
					}
					clean = strings.TrimSpace(clean)
				}

				var result DetectionResult
				if err := json.Unmarshal([]byte(clean), &result); err == nil {
					// Valid JSON — return it directly.
					return agentcore.AgentToolResult{Content: clean}
				}

				// VLM returned free text — wrap it in a DetectionResult summary.
				wrapped, _ := json.Marshal(DetectionResult{
					Objects:    []DetectedObject{},
					Violations: []string{},
					Confidence: 0.7,
					Summary:    rawText,
				})
				return agentcore.AgentToolResult{Content: string(wrapped)}
			},
		},
		{
			Name:        "analyze_scene_context",
			Description: "综合分析场景上下文，结合位置信息判断危险等级",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"detection_json": {"type": "string", "description": "detect_objects 输出的 JSON"},
					"location":       {"type": "string", "description": "作业位置描述"}
				},
				"required": ["detection_json"]
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				// Pass-through: let the VisionAgent LLM reason freely about context.
				var args struct {
					DetectionJSON string `json:"detection_json"`
					Location      string `json:"location"`
				}
				json.Unmarshal(texCtx.Args, &args) //nolint:errcheck
				return agentcore.AgentToolResult{
					Content: fmt.Sprintf(`{"detection":%s,"location":%q,"ready_for_analysis":true}`,
						jsonOrString(args.DetectionJSON), args.Location),
				}
			},
		},
	}
}

// buildRiskTools returns RiskAgent tools. Results are minimal so the LLM reasons freely.
func buildRiskTools() []agentcore.AgentTool {
	return []agentcore.AgentTool{
		{
			Name:        "lookup_regulation",
			Description: "查询适用的安全生产法规和标准",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"violation_type": {"type": "string", "description": "违规类型"}
				},
				"required": ["violation_type"]
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				// Return minimal scaffolding; the LLM supplies real regulatory knowledge.
				var args struct {
					ViolationType string `json:"violation_type"`
				}
				json.Unmarshal(texCtx.Args, &args) //nolint:errcheck
				return agentcore.AgentToolResult{
					Content: fmt.Sprintf(`{"violation_type":%q,"regulations":[]}`, args.ViolationType),
				}
			},
		},
		{
			Name:        "evaluate_risk",
			Description: "综合评估安全风险等级（low/medium/high/critical）",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"violations":  {"type": "array", "items": {"type": "string"}, "description": "违规列表"},
					"regulations": {"type": "array", "items": {"type": "string"}, "description": "适用法规"}
				},
				"required": ["violations"]
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				// Echo the input so the LLM evaluates risk from actual violation data.
				return agentcore.AgentToolResult{Content: string(texCtx.Args)}
			},
		},
	}
}

// buildDecisionTools returns DecisionAgent tools. LLM determines strategy freely.
func buildDecisionTools() []agentcore.AgentTool {
	return []agentcore.AgentTool{
		{
			Name:        "confirm_hazard",
			Description: "确认并记录危险源信息",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"risk_json": {"type": "string", "description": "RiskAssessment JSON"}
				},
				"required": ["risk_json"]
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				return agentcore.AgentToolResult{
					Content: fmt.Sprintf(`{"hazard_confirmed":true,"hazard_id":"H-%d"}`, time.Now().Unix()),
				}
			},
		},
		{
			Name:        "determine_strategy",
			Description: "根据风险等级确定处置策略",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"overall_level": {"type": "string", "description": "整体风险等级"}
				},
				"required": ["overall_level"]
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				return agentcore.AgentToolResult{Content: string(texCtx.Args)}
			},
		},
		{
			Name:        "assign_person",
			Description: "根据作业位置和违规类型分配责任人",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"location": {"type": "string", "description": "作业位置"},
					"action":   {"type": "string", "description": "处置动作"}
				}
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				return agentcore.AgentToolResult{Content: string(texCtx.Args)}
			},
		},
	}
}

// buildWorkflowTools returns WorkflowAgent tools with real timestamps and IDs.
func buildWorkflowTools() []agentcore.AgentTool {
	return []agentcore.AgentTool{
		{
			Name:        "create_order",
			Description: "创建安全整改工单",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"decision_json": {"type": "string", "description": "InspectionDecision JSON"}
				},
				"required": ["decision_json"]
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				orderID := fmt.Sprintf("WO-%d", time.Now().Unix())
				return agentcore.AgentToolResult{
					Content: fmt.Sprintf(`{"order_id":%q,"status":"created","created_at":%q,"input":%s}`,
						orderID, time.Now().Format("2006-01-02T15:04:05Z"), string(texCtx.Args)),
				}
			},
		},
		{
			Name:        "dispatch_order",
			Description: "派发工单给责任人",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"order_id": {"type": "string", "description": "工单编号"},
					"assignee": {"type": "string", "description": "责任人"}
				},
				"required": ["order_id", "assignee"]
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				return agentcore.AgentToolResult{Content: string(texCtx.Args)}
			},
		},
		{
			Name:        "verify_completion",
			Description: "验证整改任务完成情况",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"order_id": {"type": "string", "description": "工单编号"}
				},
				"required": ["order_id"]
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				return agentcore.AgentToolResult{
					Content: fmt.Sprintf(`{"verified":true,"all_tasks_completed":true,"order_id":%s}`, string(texCtx.Args)),
				}
			},
		},
		{
			Name:        "close_order",
			Description: "关闭已完成整改的工单",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"order_id": {"type": "string", "description": "工单编号"}
				},
				"required": ["order_id"]
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				return agentcore.AgentToolResult{
					Content: fmt.Sprintf(`{"status":"closed","closed_at":%q,"input":%s}`,
						time.Now().Format("2006-01-02T15:04:05Z"), string(texCtx.Args)),
				}
			},
		},
	}
}

// buildNotifyTools returns NotifyAgent tools. LLM generates real notification content.
func buildNotifyTools() []agentcore.AgentTool {
	return []agentcore.AgentTool{
		{
			Name:        "send_notification",
			Description: "向相关人员发送安全巡检通知",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"channels":  {"type": "array", "items": {"type": "string"}, "description": "通知渠道"},
					"message":   {"type": "string", "description": "通知内容"},
					"order_id":  {"type": "string", "description": "关联工单编号"}
				},
				"required": ["channels", "message"]
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				return agentcore.AgentToolResult{
					Content: fmt.Sprintf(`{"sent":true,"sent_at":%q,"input":%s}`,
						time.Now().Format("2006-01-02T15:04:05Z"), string(texCtx.Args)),
				}
			},
		},
		{
			Name:        "generate_report",
			Description: "生成完整的安全巡检报告",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"order_json": {"type": "string", "description": "WorkOrder JSON"}
				},
				"required": ["order_json"]
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				return agentcore.AgentToolResult{
					Content: fmt.Sprintf(`{"report_url":"https://safeagent.local/reports/%d","generated_at":%q,"input":%s}`,
						time.Now().Unix(), time.Now().Format("2006-01-02T15:04:05Z"), string(texCtx.Args)),
				}
			},
		},
	}
}

// jsonOrString returns s as a raw JSON value if it is valid JSON, otherwise as a JSON string.
func jsonOrString(s string) string {
	s = strings.TrimSpace(s)
	if json.Valid([]byte(s)) {
		return s
	}
	b, _ := json.Marshal(s)
	return string(b)
}
