// Package safeagent implements a multi-agent safety inspection orchestrator.
// It uses agentcore as the execution core and tools/agenttool for sub-agent dispatch.
package safeagent

import "github.com/ubuildingagent/backend/llmprovider"

// Sub-agent type constants used in Task(subagent_type=...) calls.
const (
	AgentTypeVision   = "vision_agent"
	AgentTypeRisk     = "risk_agent"
	AgentTypeDecision = "decision_agent"
	AgentTypeWorkflow = "workflow_agent"
	AgentTypeNotify   = "notify_agent"
)

// Config holds the options for building a SafeAgent orchestrator.
type Config struct {
	// APIKey for the LLM provider.
	APIKey string

	// Model to use for the orchestrator and all sub-agents.
	Model llmprovider.Model

	// VLMModel is the vision model used by the VisionAgent for image analysis.
	// When zero-value, detect_objects falls back to text-only mode.
	VLMModel llmprovider.Model

	// VLMAPIKey is the API key for the VLM provider.
	VLMAPIKey string

	// SubAgentMaxIter caps each sub-agent's iteration budget (default: 10).
	SubAgentMaxIter int

	// OrchestratorMaxIter caps the orchestrator's iteration budget (default: 20).
	OrchestratorMaxIter int
}

// SubAgentConfig is the shared config passed to each sub-agent constructor.
type SubAgentConfig struct {
	Model     llmprovider.Model
	APIKey    string
	MaxIter   int
	VLMModel  llmprovider.Model // vision model for VisionAgent
	VLMAPIKey string
}

// SceneInput is the initial request payload for a safety inspection.
type SceneInput struct {
	Description string `json:"scene_description"`
	ImageURL    string `json:"image_url,omitempty"`
	Location    string `json:"location,omitempty"`
}

// DetectedObject represents a single object identified in the scene.
type DetectedObject struct {
	Type        string  `json:"type"`
	BoundingBox string  `json:"bounding_box,omitempty"`
	Confidence  float64 `json:"confidence"`
}

// DetectionResult is the output of the VisionAgent.
type DetectionResult struct {
	Objects    []DetectedObject `json:"objects"`
	Violations []string         `json:"violations"`
	Confidence float64          `json:"confidence"`
	Summary    string           `json:"summary"`
}

// RiskItem describes a single risk entry.
type RiskItem struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	Level       string `json:"level"` // "low" | "medium" | "high" | "critical"
	Regulation  string `json:"regulation,omitempty"`
}

// RiskAssessment is the output of the RiskAgent.
type RiskAssessment struct {
	Risks        []RiskItem `json:"risks"`
	OverallLevel string     `json:"overall_level"`
	Summary      string     `json:"summary"`
}

// InspectionDecision is the output of the DecisionAgent.
type InspectionDecision struct {
	Action    string   `json:"action"` // "immediate_stop" | "rectify" | "monitor" | "pass"
	Assignee  string   `json:"assignee,omitempty"`
	Deadline  string   `json:"deadline,omitempty"`
	Steps     []string `json:"steps"`
	Rationale string   `json:"rationale"`
}

// WorkOrder is the output of the WorkflowAgent.
type WorkOrder struct {
	ID        string   `json:"id"`
	Status    string   `json:"status"` // "created" | "dispatched" | "verified" | "closed"
	Assignee  string   `json:"assignee"`
	Tasks     []string `json:"tasks"`
	CreatedAt string   `json:"created_at"`
	ClosedAt  string   `json:"closed_at,omitempty"`
}

// NotificationResult is the output of the NotifyAgent.
type NotificationResult struct {
	Channels  []string `json:"channels"`
	ReportURL string   `json:"report_url,omitempty"`
	Summary   string   `json:"summary"`
}

// InspectionContext accumulates outputs across all pipeline stages.
type InspectionContext struct {
	Input        SceneInput          `json:"input"`
	Detection    *DetectionResult    `json:"detection,omitempty"`
	Risk         *RiskAssessment     `json:"risk,omitempty"`
	Decision     *InspectionDecision `json:"decision,omitempty"`
	WorkOrder    *WorkOrder          `json:"work_order,omitempty"`
	Notification *NotificationResult `json:"notification,omitempty"`
}
