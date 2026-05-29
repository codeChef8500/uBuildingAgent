import { useState } from 'react'
import type { ToolCallEntry } from '../types/agent'

interface Props {
  tool: ToolCallEntry
}

function tryFormat(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2)
  } catch {
    return s
  }
}

export function ToolCallRow({ tool }: Props) {
  const [expanded, setExpanded] = useState(false)

  return (
    <div className="border border-gray-200 rounded-lg overflow-hidden text-xs">
      <button
        onClick={() => setExpanded((v) => !v)}
        className="w-full flex items-center gap-2 px-3 py-1.5 bg-gray-100 hover:bg-gray-200 transition-colors text-left"
      >
        {tool.running ? (
          <span className="w-3 h-3 rounded-full border border-blue-400 border-t-transparent animate-spin shrink-0" />
        ) : (
          <span className="w-3 h-3 rounded-full bg-green-500 shrink-0" />
        )}
        <code className="text-indigo-600 font-mono">{tool.name}</code>
        <span className="text-gray-500 truncate flex-1">{tool.args.slice(0, 60)}{tool.args.length > 60 ? '…' : ''}</span>
        <span className="text-gray-400 ml-auto">{expanded ? '▲' : '▼'}</span>
      </button>
      {expanded && (
        <div className="px-3 py-2 bg-white space-y-2">
          <div>
            <p className="text-gray-500 mb-1 font-semibold">参数</p>
            <pre className="text-gray-700 whitespace-pre-wrap font-mono text-xs overflow-x-auto">
              {tryFormat(tool.args)}
            </pre>
          </div>
          {tool.result !== undefined && (
            <div>
              <p className="text-gray-500 mb-1 font-semibold">结果</p>
              <pre className="text-green-700 whitespace-pre-wrap font-mono text-xs overflow-x-auto max-h-40 overflow-y-auto">
                {tryFormat(tool.result)}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
