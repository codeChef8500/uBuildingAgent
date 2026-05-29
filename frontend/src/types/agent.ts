export type AgentEventType =
  | 'text_delta'
  | 'tool_start'
  | 'tool_end'
  | 'turn_end'
  | 'error'
  | 'context_patch'

export interface ToolCall {
  id: string
  name: string
  args: string
}

export interface AgentEvent {
  type: AgentEventType
  delta?: string
  tool_call?: ToolCall
  tool_result?: string
  err?: string
}

export type SubAgentStatus = 'pending' | 'running' | 'done' | 'error'

export const PIPELINE_STAGES = [
  { key: 'vision_agent',   label: '视觉识别', icon: '👁' },
  { key: 'risk_agent',     label: '风险分析', icon: '⚠️' },
  { key: 'decision_agent', label: '处置决策', icon: '⚖️' },
  { key: 'workflow_agent', label: '工单闭环', icon: '📋' },
  { key: 'notify_agent',   label: '通知上报', icon: '📢' },
] as const

export type SubAgentKey = typeof PIPELINE_STAGES[number]['key']

export interface ToolCallEntry {
  id: string
  name: string
  args: string
  result?: string
  running: boolean
}

export interface SubAgentState {
  key: SubAgentKey
  status: SubAgentStatus
  taskCallId?: string
  tools: ToolCallEntry[]
  output?: string
}

export interface DetectedObject {
  type: string
  bounding_box?: string
  confidence: number
}

export interface DetectionResult {
  objects: DetectedObject[]
  violations: string[]
  confidence: number
  summary: string
}

export interface RiskItem {
  code: string
  description: string
  level: 'low' | 'medium' | 'high' | 'critical'
  regulation?: string
}

export interface RiskAssessment {
  risks: RiskItem[]
  overall_level: string
  summary: string
}

export interface InspectionDecision {
  action: 'immediate_stop' | 'rectify' | 'monitor' | 'pass'
  assignee?: string
  deadline?: string
  steps: string[]
  rationale: string
}

export interface WorkOrder {
  id: string
  status: string
  assignee: string
  tasks: string[]
  created_at: string
  closed_at?: string
}

export interface NotificationResult {
  channels: string[]
  report_url?: string
  summary: string
}

export interface InspectionContext {
  input: { scene_description: string; image_url?: string; location?: string }
  detection?: DetectionResult
  risk?: RiskAssessment
  decision?: InspectionDecision
  work_order?: WorkOrder
  notification?: NotificationResult
}
