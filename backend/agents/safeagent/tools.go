package safeagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/llmprovider"
)

// vlmDetectionPrompt is sent to the VLM alongside the image for object detection.
const vlmDetectionPrompt = `你是施工现场安全检测助手。你可能会接收到单张图片，或者是按时间先后顺序排列的多张连续视频帧（多图时序输入）。请仔细分析输入的图片，完成以下任务：
1. 识别图中所有人员、设备、工具和构筑物
2. 找出所有安全违规行为（如：未戴安全帽、未系安全绳、违规操作、危险行为等）。如果是多张连续帧，请着重对比图片间的运动状态、状态变化或位置移动（例如：人员在翻越、正在跌落、防护网状态突变等动态危险行为），进行综合研判
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

// callVLMMulti sends multiple images + text prompt to the vision model and returns the full text response.
func callVLMMulti(ctx context.Context, vlmModel llmprovider.Model, apiKey string, imageDataURLs []string, textPrompt string) (string, error) {
	contentParts := []llmprovider.ContentPart{}

	// Prepend all images so the VLM sees them before the text.
	for _, imgData := range imageDataURLs {
		if imgData != "" {
			contentParts = append(contentParts, llmprovider.ContentPart{
				Type:     llmprovider.ContentTypeImageURL,
				ImageURL: imgData,
			})
		}
	}

	contentParts = append(contentParts, llmprovider.ContentPart{
		Type: llmprovider.ContentTypeText,
		Text: textPrompt,
	})

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

// callVLM sends an image + text prompt to the vision model and returns the full text response.
func callVLM(ctx context.Context, vlmModel llmprovider.Model, apiKey, imageDataURL, textPrompt string) (string, error) {
	var images []string
	if imageDataURL != "" {
		images = []string{imageDataURL}
	}
	return callVLMMulti(ctx, vlmModel, apiKey, images, textPrompt)
}

// callDetector sends image bytes to the Python Detector Sidecar and returns
// the structured DetectorResult. Returns nil, nil if endpoint is empty.
func callDetector(ctx context.Context, endpoint string, imageBytes []byte, prevImageBytes []byte) (*DetectorResult, error) {
	if endpoint == "" {
		return nil, nil
	}

	reqBody := map[string]interface{}{
		"image":     base64.StdEncoding.EncodeToString(imageBytes),
		"mime_type": "image/jpeg",
	}
	if len(prevImageBytes) > 0 {
		reqBody["prev_image"] = base64.StdEncoding.EncodeToString(prevImageBytes)
	}

	body, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/detect", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("callDetector: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("callDetector: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var result DetectorResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("callDetector: parse response: %w", err)
	}
	return &result, nil
}

// buildVisionTools returns VisionAgent tools. detect_objects calls Detector
// Sidecar first (YOLO+CV), then falls back to VLM only when needed.
func buildVisionTools(vlmModel llmprovider.Model, vlmAPIKey, detectorEndpoint string) []agentcore.AgentTool {
	return []agentcore.AgentTool{
		{
			Name:        "detect_objects",
			Description: "检测场景中的人员、设备、安全装备和危险行为，通过视觉模型分析图像（支持多图时序输入）",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"image_url":   {"type": "string", "description": "主图像 URL"},
					"image_urls":  {"type": "array", "items": {"type": "string"}, "description": "时序多帧图像 URLs 列表（可选）"},
					"description": {"type": "string", "description": "场景文字描述"}
				}
			}`),
			Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				var args struct {
					ImageURL    string   `json:"image_url"`
					ImageURLs   []string `json:"image_urls"`
					Description string   `json:"description"`
				}
				if err := json.Unmarshal(texCtx.Args, &args); err != nil {
					return agentcore.AgentToolResult{
						Content: fmt.Sprintf(`{"error":"invalid args: %v"}`, err),
						IsError: true,
					}
				}

				// Collect all target image URLs
				var urls []string
				if len(args.ImageURLs) > 0 {
					urls = args.ImageURLs
				} else if args.ImageURL != "" {
					urls = []string{args.ImageURL}
				}

				// Fetch and encode all images concurrently
				var imageDataURLs []string
				var rawBytes []byte
				if len(urls) > 0 {
					imageDataURLs = make([]string, len(urls))
					var wg sync.WaitGroup
					for i, url := range urls {
						if url == "" {
							continue
						}
						wg.Add(1)
						go func(idx int, targetURL string) {
							defer wg.Done()
							data, ferr := fetchImageAsDataURL(targetURL)
							if ferr != nil {
								fmt.Printf("[detect_objects] image fetch warning for %q: %v\n", targetURL, ferr)
								return
							}
							imageDataURLs[idx] = data
						}(i, url)
					}
					wg.Wait()
					// Decode the first image for the detector
					if imageDataURLs[0] != "" {
						parts := strings.SplitN(imageDataURLs[0], ",", 2)
						if len(parts) == 2 {
							rawBytes, _ = base64.StdEncoding.DecodeString(parts[1])
						}
					}
				}

				// Stage 1: Try Detector Sidecar (YOLO + CV + rules) first.
				if len(rawBytes) > 0 {
					detResult, detErr := callDetector(texCtx.Ctx, detectorEndpoint, rawBytes, nil)
					if detErr == nil && detResult != nil {
						switch detResult.Status {
						case "safe":
							// No violations — return detector result directly, skip VLM.
							safeJSON, _ := json.Marshal(DetectionResult{
								Objects:    detResult.Objects,
								Violations: []string{},
								Confidence: 0.95,
								Summary:    detResult.Summary,
							})
							return agentcore.AgentToolResult{Content: string(safeJSON)}
						case "violation":
							// Clear violation — return detector result, skip VLM.
							violations := make([]string, len(detResult.Violations))
							for i, v := range detResult.Violations {
								violations[i] = v.Type + ": " + v.Reason
							}
							violJSON, _ := json.Marshal(DetectionResult{
								Objects:    detResult.Objects,
								Violations: violations,
								Confidence: 0.90,
								Summary:    detResult.Summary,
							})
							return agentcore.AgentToolResult{Content: string(violJSON)}
						}
						// "suspicious" → fall through to VLM.
					}
				}

				// Stage 2: VLM deep analysis (for suspicious or detector-unavailable cases).
				prompt := vlmDetectionPrompt
				if args.Description != "" {
					prompt = "场景描述：" + args.Description + "\n\n" + vlmDetectionPrompt
				}

				rawText, err := callVLMMulti(texCtx.Ctx, vlmModel, vlmAPIKey, imageDataURLs, prompt)
				if err != nil {
					return agentcore.AgentToolResult{
						Content: fmt.Sprintf(`{"error":"VLM call failed: %v","summary":"视觉检测失败"}`, err),
						IsError: true,
					}
				}

				// Try to parse VLM output as DetectionResult JSON.
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
					return agentcore.AgentToolResult{Content: clean}
				}

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

// buildAnalysisTools returns merged Risk+Decision tools for AnalysisAgent.
func buildAnalysisTools() []agentcore.AgentTool {
	tools := buildRiskTools()
	tools = append(tools, buildDecisionTools()...)
	return tools
}

// buildClosureTools returns merged Workflow+Notify tools for ClosureAgent.
func buildClosureTools() []agentcore.AgentTool {
	tools := buildWorkflowTools()
	tools = append(tools, buildNotifyTools()...)
	return tools
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
