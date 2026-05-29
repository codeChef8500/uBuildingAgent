import type { SubAgentState, SubAgentStatus } from '../types/agent'
import { PIPELINE_STAGES } from '../types/agent'

interface Props {
  subAgents: SubAgentState[]
}

function statusDot(status: SubAgentStatus) {
  switch (status) {
    case 'running':
      return (
        <span className="w-5 h-5 rounded-full border-2 border-blue-400 border-t-transparent animate-spin inline-block" />
      )
    case 'done':
      return <span className="w-5 h-5 rounded-full bg-green-500 flex items-center justify-center text-white text-xs">✓</span>
    case 'error':
      return <span className="w-5 h-5 rounded-full bg-red-500 flex items-center justify-center text-white text-xs">✗</span>
    default:
      return <span className="w-5 h-5 rounded-full bg-gray-300 inline-block" />
  }
}

export function PipelineProgress({ subAgents }: Props) {
  const stateMap = new Map(subAgents.map((s) => [s.key, s.status]))

  return (
    <div className="flex items-center gap-0">
      {PIPELINE_STAGES.map((stage, i) => {
        const status = stateMap.get(stage.key) ?? 'pending'
        return (
          <div key={stage.key} className="flex items-center flex-1 min-w-0">
            <div className="flex flex-col items-center gap-1 min-w-0 flex-1">
              <div className="flex items-center justify-center">{statusDot(status)}</div>
              <span className={`text-xs text-center leading-tight truncate w-full px-1 ${
                status === 'running' ? 'text-blue-600' :
                status === 'done' ? 'text-green-600' :
                status === 'error' ? 'text-red-600' : 'text-gray-400'
              }`}>
                {stage.icon} {stage.label}
              </span>
            </div>
            {i < PIPELINE_STAGES.length - 1 && (
              <div className={`h-px flex-1 mx-1 ${
                status === 'done' ? 'bg-green-400' : 'bg-gray-300'
              }`} />
            )}
          </div>
        )
      })}
    </div>
  )
}
