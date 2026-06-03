// Package safeagent implements a multi-agent safety inspection orchestrator.
// It uses agentcore as the execution core and tools/agenttool for sub-agent dispatch.
package safeagent

import "github.com/ubuildingagent/backend/llmprovider"

// Sub-agent type constants used in Task(subagent_type=...) calls.
// Pipeline reduced from 5 stages to 3: Vision → Analysis → Closure.
const (
	AgentTypeVision   = "vision_agent"
	AgentTypeAnalysis = "analysis_agent" // merged Risk + Decision
	AgentTypeClosure  = "closure_agent"  // merged Workflow + Notify

	// Deprecated: kept for backward compatibility.
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

	// DetectorEndpoint is the URL of the Python Detector Sidecar (YOLO + CV).
	// e.g. "http://localhost:9000". Empty = skip detector, use VLM directly.
	DetectorEndpoint string

	// SubAgentMaxIter caps each sub-agent's iteration budget (default: 10).
	SubAgentMaxIter int

	// OrchestratorMaxIter caps the orchestrator's iteration budget (default: 20).
	OrchestratorMaxIter int
}

// SubAgentConfig is the shared config passed to each sub-agent constructor.
type SubAgentConfig struct {
	Model            llmprovider.Model
	APIKey           string
	MaxIter          int
	VLMModel         llmprovider.Model // vision model for VisionAgent
	VLMAPIKey        string
	DetectorEndpoint string // Detector Sidecar URL
}

// SceneInput is the initial request payload for a safety inspection.
type SceneInput struct {
	Description string   `json:"scene_description"`
	ImageURL    string   `json:"image_url,omitempty"`
	ImageURLs   []string `json:"image_urls,omitempty"`
	Location    string   `json:"location,omitempty"`
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

// AnalysisResult merges RiskAssessment + InspectionDecision into a single output.
// Produced by AnalysisAgent in one LLM call instead of two.
type AnalysisResult struct {
	OverallLevel string     `json:"overall_level"` // low/medium/high/critical
	Action       string     `json:"action"`        // immediate_stop/rectify/monitor/pass
	Assignee     string     `json:"assignee,omitempty"`
	Deadline     string     `json:"deadline,omitempty"`
	Steps        []string   `json:"steps"`
	Rationale    string     `json:"rationale"`
	Risks        []RiskItem `json:"risks"`
	Summary      string     `json:"summary"`
}

// ClosureResult merges WorkOrder + NotificationResult into a single output.
// Produced by ClosureAgent in one LLM call instead of two.
type ClosureResult struct {
	OrderID   string   `json:"order_id"`
	Status    string   `json:"status"`
	Assignee  string   `json:"assignee"`
	Tasks     []string `json:"tasks"`
	CreatedAt string   `json:"created_at"`
	ClosedAt  string   `json:"closed_at,omitempty"`
	Channels  []string `json:"channels"`
	ReportURL string   `json:"report_url,omitempty"`
	Summary   string   `json:"summary"`
}

// DetectorResult is the structured output from the Python Detector Sidecar.
type DetectorResult struct {
	Status            string              `json:"status"` // safe/violation/suspicious
	Objects           []DetectedObject    `json:"objects"`
	Violations        []DetectorViolation `json:"violations"`
	StructuralHazards []StructuralHazard  `json:"structural_hazards,omitempty"`
	Motion            MotionAnalysis      `json:"motion,omitempty"`
	ShouldInvokeVLM   bool                `json:"should_invoke_vlm"`
	Summary           string              `json:"summary"`
}

// DetectorViolation is a single violation found by the Detector Sidecar.
type DetectorViolation struct {
	Type       string  `json:"type"`
	Severity   string  `json:"severity"`
	PersonID   string  `json:"person_id,omitempty"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
	Regulation string  `json:"regulation,omitempty"`
}

// StructuralHazard represents a structural hazard detected by traditional CV.
type StructuralHazard struct {
	Type         string  `json:"type"`
	Line         [][]int `json:"line,omitempty"`
	HasGuardrail bool    `json:"has_guardrail"`
}

// MotionAnalysis captures frame-diff and optical flow results.
type MotionAnalysis struct {
	DiffRatio float64  `json:"diff_ratio"`
	Alerts    []string `json:"alerts"`
}

// InspectionContext accumulates outputs across all pipeline stages.
type InspectionContext struct {
	Input     SceneInput       `json:"input"`
	Detection *DetectionResult `json:"detection,omitempty"`
	Analysis  *AnalysisResult  `json:"analysis,omitempty"`
	Closure   *ClosureResult   `json:"closure,omitempty"`

	// Deprecated: kept for backward compatibility.
	Risk         *RiskAssessment     `json:"risk,omitempty"`
	Decision     *InspectionDecision `json:"decision,omitempty"`
	WorkOrder    *WorkOrder          `json:"work_order,omitempty"`
	Notification *NotificationResult `json:"notification,omitempty"`
}
