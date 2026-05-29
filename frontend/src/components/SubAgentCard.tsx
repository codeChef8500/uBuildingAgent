import { useState, useEffect } from 'react'
import type { SubAgentState } from '../types/agent'
import { PIPELINE_STAGES } from '../types/agent'
import { ToolCallRow } from './ToolCallRow'

interface Props {
  agent: SubAgentState
}

const STATUS_BADGE: Record<string, { cls: string; text: string }> = {
  pending: { cls: 'bg-gray-100 text-gray-400', text: '等待中' },
  running: { cls: 'bg-blue-100 text-blue-600 animate-pulse', text: '运行中' },
  done: { cls: 'bg-green-100 text-green-600', text: '已完成' },
  error: { cls: 'bg-red-100 text-red-600', text: '失败' },
}

export function SubAgentCard({ agent }: Props) {
  const stage = PIPELINE_STAGES.find((s) => s.key === agent.key)
  const badge = STATUS_BADGE[agent.status]

  // Auto-expand when running, keep expanded when done
  const [open, setOpen] = useState(false)
  useEffect(() => {
    if (agent.status === 'running') setOpen(true)
  }, [agent.status])

  const isActive = agent.status === 'running' || agent.status === 'done'

  return (
    <div className={`rounded-lg border overflow-hidden transition-all ${
      agent.status === 'running' ? 'border-blue-400' :
      agent.status === 'done' ? 'border-green-400' :
      agent.status === 'error' ? 'border-red-400' :
      'border-gray-200'
    }`}>
      <button
        onClick={() => isActive && setOpen((v) => !v)}
        className={`w-full flex items-center gap-3 px-3 py-2.5 text-left transition-colors ${
          isActive ? 'hover:bg-gray-50' : 'cursor-default'
        } bg-white`}
      >
        <span className="text-base">{stage?.icon ?? '🤖'}</span>
        <span className={`text-sm font-medium ${isActive ? 'text-gray-800' : 'text-gray-400'}`}>
          {stage?.label ?? agent.key}
        </span>
        <span className={`ml-auto text-xs px-2 py-0.5 rounded-full ${badge.cls}`}>{badge.text}</span>
        {isActive && (
          <span className="text-gray-400 text-xs ml-1">{open ? '▲' : '▼'}</span>
        )}
      </button>

      {open && (
        <div className="px-3 pb-3 pt-1 space-y-1.5 bg-gray-50">
          {agent.tools.length === 0 && agent.status === 'running' && (
            <p className="text-gray-400 text-xs italic">等待工具调用...</p>
          )}
          {agent.tools.map((t) => (
            <ToolCallRow key={t.id} tool={t} />
          ))}
          {agent.output && (
            <div className="mt-2 border-t border-gray-200 pt-2">
              <p className="text-xs text-gray-400 mb-1">输出</p>
              <pre className="text-xs text-gray-700 whitespace-pre-wrap font-mono max-h-32 overflow-y-auto">
                {(() => {
                  try { return JSON.stringify(JSON.parse(agent.output), null, 2) }
                  catch { return agent.output }
                })()}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
