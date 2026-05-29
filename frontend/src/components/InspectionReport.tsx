import type { ReactNode } from 'react'
import type { InspectionContext, RiskItem } from '../types/agent'

interface Props {
  report: InspectionContext
}

const RISK_COLORS: Record<string, string> = {
  critical: 'bg-red-50 text-red-700 border-red-200',
  high: 'bg-orange-50 text-orange-700 border-orange-200',
  medium: 'bg-amber-50 text-amber-700 border-amber-200',
  low: 'bg-green-50 text-green-700 border-green-200',
}
const RISK_LABELS: Record<string, string> = {
  critical: '严重', high: '高危', medium: '中危', low: '低危',
}

const ACTION_COLORS: Record<string, string> = {
  immediate_stop: 'bg-red-100 text-red-700',
  rectify: 'bg-orange-100 text-orange-700',
  monitor: 'bg-amber-100 text-amber-700',
  pass: 'bg-green-100 text-green-700',
}
const ACTION_LABELS: Record<string, string> = {
  immediate_stop: '立即停工', rectify: '限期整改', monitor: '监控观察', pass: '通过',
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="bg-white rounded-lg border border-gray-200 p-4 space-y-2 shadow-sm">
      <h3 className="text-xs font-semibold text-gray-400 uppercase tracking-wider">{title}</h3>
      {children}
    </div>
  )
}

function RiskBadge({ item }: { item: RiskItem }) {
  return (
    <div className={`rounded border p-2 space-y-1 ${RISK_COLORS[item.level] ?? 'bg-gray-100 text-gray-700 border-gray-200'}`}>
      <div className="flex items-center gap-2">
        <span className="text-xs font-mono">{item.code}</span>
        <span className={`text-xs px-1.5 py-0.5 rounded font-medium ${RISK_COLORS[item.level]}`}>
          {RISK_LABELS[item.level] ?? item.level}
        </span>
      </div>
      <p className="text-xs">{item.description}</p>
      {item.regulation && <p className="text-xs opacity-70">依据：{item.regulation}</p>}
    </div>
  )
}

export function InspectionReport({ report }: Props) {
  const { detection, risk, decision, work_order, notification } = report

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 mb-1">
        <span className="w-2 h-2 rounded-full bg-green-500" />
        <h2 className="text-sm font-bold text-gray-900">巡检报告</h2>
      </div>

      {/* Detection */}
      {detection && (
        <Section title="视觉检测结果">
          <p className="text-xs text-gray-700">{detection.summary}</p>
          {detection.violations.length > 0 && (
            <ul className="space-y-1 mt-1">
              {detection.violations.map((v, i) => (
                <li key={i} className="flex items-start gap-1.5 text-xs text-red-600">
                  <span className="mt-0.5">•</span>{v}
                </li>
              ))}
            </ul>
          )}
          <p className="text-xs text-gray-400">检测置信度：{(detection.confidence * 100).toFixed(0)}%</p>
        </Section>
      )}

      {/* Risk */}
      {risk && (
        <Section title="风险评估">
          <div className="flex items-center gap-2 mb-2">
            <span className="text-xs text-gray-500">整体风险等级</span>
            <span className={`text-xs px-2 py-0.5 rounded font-medium ${RISK_COLORS[risk.overall_level] ?? 'bg-gray-100 text-gray-700 border-gray-200'}`}>
              {RISK_LABELS[risk.overall_level] ?? risk.overall_level}
            </span>
          </div>
          <p className="text-xs text-gray-700 mb-2">{risk.summary}</p>
          <div className="space-y-1.5">
            {risk.risks.map((r, i) => <RiskBadge key={i} item={r} />)}
          </div>
        </Section>
      )}

      {/* Decision */}
      {decision && (
        <Section title="处置决策">
          <div className="flex items-center gap-2 mb-1">
            <span className={`text-xs px-2 py-0.5 rounded font-medium ${ACTION_COLORS[decision.action] ?? 'bg-slate-700 text-slate-300'}`}>
              {ACTION_LABELS[decision.action] ?? decision.action}
            </span>
            {decision.assignee && <span className="text-xs text-gray-500">责任人：{decision.assignee}</span>}
            {decision.deadline && <span className="text-xs text-gray-500">截止：{decision.deadline}</span>}
          </div>
          <p className="text-xs text-gray-700 mb-2">{decision.rationale}</p>
          {decision.steps.length > 0 && (
            <ol className="space-y-1">
              {decision.steps.map((step, i) => (
                <li key={i} className="flex items-start gap-1.5 text-xs text-gray-700">
                  <span className="text-indigo-600 font-mono shrink-0">{i + 1}.</span>{step}
                </li>
              ))}
            </ol>
          )}
        </Section>
      )}

      {/* Work order */}
      {work_order && (
        <Section title="工单">
          <div className="flex items-center gap-3 text-xs">
            <span className="text-gray-500 font-mono">{work_order.id}</span>
            <span className="text-gray-700">责任人：{work_order.assignee}</span>
            <span className="text-gray-400">创建：{work_order.created_at}</span>
          </div>
          <ul className="mt-1 space-y-1">
            {work_order.tasks.map((t, i) => (
              <li key={i} className="flex items-start gap-1.5 text-xs text-gray-700">
                <span className="text-indigo-600 shrink-0">▸</span>{t}
              </li>
            ))}
          </ul>
        </Section>
      )}

      {/* Notification */}
      {notification && (
        <Section title="通知上报">
          <p className="text-xs text-gray-700">{notification.summary}</p>
          <div className="flex flex-wrap gap-1 mt-1">
            {notification.channels.map((c, i) => (
              <span key={i} className="text-xs bg-gray-100 text-gray-600 px-2 py-0.5 rounded">{c}</span>
            ))}
          </div>
          {notification.report_url && (
            <a href={notification.report_url} target="_blank" rel="noreferrer"
               className="text-xs text-indigo-600 hover:underline block mt-1">
              {notification.report_url}
            </a>
          )}
        </Section>
      )}
    </div>
  )
}
