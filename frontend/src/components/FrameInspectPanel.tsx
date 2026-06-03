import { useEffect, useRef, useState } from 'react'
import { InspectionReport } from './InspectionReport'
import type { FrameEntry, FrameStatus, ToolActivity, VideoInspectState } from '../hooks/useVideoInspect'

// ── Helpers ────────────────────────────────────────────────────────────────

function fmtTime(s: number): string {
  const m = Math.floor(s / 60)
  const sec = Math.floor(s % 60)
  return `${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`
}

const STATUS_ICON: Record<FrameStatus, string> = {
  queued:  '⏳',
  running: '🔄',
  done:    '✅',
  dropped: '⏭',
  error:   '❌',
}

const STATUS_COLOR: Record<FrameStatus, string> = {
  queued:  'text-gray-400',
  running: 'text-indigo-500',
  done:    'text-green-600',
  dropped: 'text-gray-300',
  error:   'text-red-500',
}

// ── Sub-components ─────────────────────────────────────────────────────────

function SimpleMarkdown({ content, running }: { content: string; running?: boolean }) {
  const lines = content.split('\n')
  return (
    <div className="space-y-1.5 text-sm text-gray-700 leading-relaxed font-sans">
      {lines.map((line, idx) => {
        const isLastLine = idx === lines.length - 1
        const trimmed = line.trim()

        const renderLineContent = (text: string) => {
          return (
            <>
              {parseBold(text)}
              {isLastLine && running && (
                <span className="inline-block w-1.5 h-3.5 bg-indigo-500 ml-1 animate-pulse align-middle rounded-sm" />
              )}
            </>
          )
        }

        // 1. Headers
        if (trimmed.startsWith('###')) {
          return <h4 key={idx} className="text-sm font-bold text-gray-900 mt-3 mb-1.5">{renderLineContent(trimmed.slice(3).trim())}</h4>
        }
        if (trimmed.startsWith('##')) {
          return <h3 key={idx} className="text-base font-bold text-gray-900 mt-4 mb-2 border-b border-gray-100 pb-1">{renderLineContent(trimmed.slice(2).trim())}</h3>
        }
        if (trimmed.startsWith('#')) {
          return <h2 key={idx} className="text-lg font-bold text-gray-900 mt-5 mb-2.5">{renderLineContent(trimmed.slice(1).trim())}</h2>
        }

        // 2. Bullet list
        if (trimmed.startsWith('-') || trimmed.startsWith('*')) {
          return (
            <div key={idx} className="flex gap-2 items-start ml-2 my-1">
              <span className="text-indigo-500 mt-1.5 select-none shrink-0 text-[8px]">●</span>
              <span className="flex-1">{renderLineContent(trimmed.slice(1).trim())}</span>
            </div>
          )
        }

        // 3. Blockquotes
        if (trimmed.startsWith('>')) {
          return (
            <blockquote key={idx} className="border-l-4 border-indigo-200 bg-indigo-50/50 pl-3 py-1 my-2 text-xs text-indigo-800 rounded-r-lg">
              {renderLineContent(trimmed.slice(1).trim())}
            </blockquote>
          )
        }

        // 4. Empty line
        if (!trimmed) {
          return <div key={idx} className="h-1.5" />
        }

        // 5. Normal Paragraph
        return <p key={idx} className="my-1">{renderLineContent(trimmed)}</p>
      })}
    </div>
  )
}

function parseBold(text: string): React.ReactNode[] {
  const parts = text.split('**')
  return parts.map((part, i) => {
    if (i % 2 === 1) {
      return <strong key={i} className="font-bold text-gray-900">{part}</strong>
    }
    return <span key={i}>{part}</span>
  })
}

function ToolRow({ tool }: { tool: ToolActivity }) {
  return (
    <div className="flex items-center gap-1.5 text-xs py-0.5">
      <span className="shrink-0">{tool.done ? '✅' : '⚙️'}</span>
      <span className="font-mono text-indigo-700 font-medium">{tool.name}</span>
      {tool.done && tool.result && (
        <span className="text-gray-400 truncate max-w-xs">{tool.result.slice(0, 80)}</span>
      )}
    </div>
  )
}

function ActiveFrameCard({ frame }: { frame: FrameEntry }) {
  const bottomRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [frame.streamingText, frame.thinkingText])

  const isRunning = frame.status === 'running'

  // Filter streaming text using [REPORT_JSON] delimiter
  let displayText = frame.streamingText
  const marker = '[REPORT_JSON]'
  const jsonStart = frame.streamingText.indexOf(marker)
  if (jsonStart !== -1) {
    displayText = frame.streamingText.slice(0, jsonStart).trim()
  } else {
    const fallbackStart = frame.streamingText.indexOf('{"input"')
    if (fallbackStart !== -1) {
      displayText = frame.streamingText.slice(0, fallbackStart).trim()
    }
  }

  return (
    <div className="flex gap-3 items-start">
      <div className="w-8 h-8 rounded-full bg-indigo-100 flex items-center justify-center text-base shrink-0">🤖</div>
      <div className="flex-1 space-y-2 min-w-0">
        {/* Frame header */}
        <div className={`border rounded-2xl rounded-tl-sm px-4 py-2.5 ${
          isRunning 
            ? 'bg-indigo-50 border-indigo-200 text-indigo-700' 
            : 'bg-green-50 border-green-200 text-green-700'
        }`}>
          <div className="flex items-center gap-2 text-xs font-medium">
            <span>🎬 {isRunning ? `正在分析第 ${frame.idx + 1} 帧` : `第 ${frame.idx + 1} 帧分析结论`}</span>
            <span className="text-current opacity-40">·</span>
            <span>{fmtTime(frame.timestamp)}</span>
            {isRunning ? (
              <span className="ml-auto text-indigo-400 animate-pulse">处理中…</span>
            ) : (
              <span className="ml-auto text-green-500">已完成 ✅</span>
            )}
          </div>
        </div>

        {/* Thinking */}
        {frame.thinkingText && isRunning && (
          <div className="bg-amber-50 border border-amber-200 rounded-2xl rounded-tl-sm px-4 py-2.5">
            <p className="text-xs text-amber-600 font-medium mb-1">💭 思考过程</p>
            <p className="text-xs text-amber-700 leading-relaxed whitespace-pre-wrap line-clamp-6">
              {frame.thinkingText}
            </p>
          </div>
        )}

        {/* Tool calls */}
        {frame.tools.length > 0 && isRunning && (
          <div className="bg-white border border-gray-200 rounded-2xl rounded-tl-sm px-4 py-2.5">
            <p className="text-xs text-gray-400 font-medium mb-1.5">🔧 工具调用</p>
            {frame.tools.map((t, i) => <ToolRow key={i} tool={t} />)}
          </div>
        )}

        {/* Streaming output (Markdown report) */}
        {displayText && (
          <div className="bg-white border border-gray-200 rounded-2xl rounded-tl-sm px-4 py-3 shadow-sm">
            <p className="text-xs text-gray-400 mb-2 font-medium">巡检报告总结</p>
            <SimpleMarkdown content={displayText} running={isRunning} />
          </div>
        )}

        {/* Final structured report card */}
        {!!frame.report && (
          <div className="bg-white border border-gray-200 rounded-2xl rounded-tl-sm p-4 shadow-sm">
            <InspectionReport report={frame.report as any} />
          </div>
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}

function HistoryFrameCard({
  frame,
  expanded,
  onToggle,
  onSeek,
}: {
  frame: FrameEntry
  expanded: boolean
  onToggle: () => void
  onSeek: () => void
}) {
  if (frame.status === 'dropped') return null
  const report = frame.report as any
  const overallLevel = report?.risk?.overall_level
  const violations = report?.detection?.violations || []
  const hasHazard = (violations.length > 0) || (['critical', 'high', 'medium'].includes(overallLevel))

  return (
    <div className="rounded-xl border border-gray-100 bg-white overflow-hidden shadow-sm transition-all duration-200">
      {/* Card Header (clickable) */}
      <div
        onClick={onToggle}
        className="flex items-center gap-2.5 px-3 py-2 cursor-pointer hover:bg-gray-50 transition-colors select-none"
      >
        <span className={STATUS_COLOR[frame.status]}>{STATUS_ICON[frame.status]}</span>
        <span className="text-gray-500 font-mono font-medium text-xs bg-gray-100 px-1.5 py-0.5 rounded">{fmtTime(frame.timestamp)}</span>
        <span className="text-gray-800 font-semibold text-xs">第 {frame.idx + 1} 帧</span>

        <span className={`text-[10px] px-1.5 py-0.5 rounded font-medium ml-2 ${
          frame.status === 'done'
            ? hasHazard ? 'bg-red-50 text-red-700 border border-red-100' : 'bg-green-50 text-green-700 border border-green-100'
            : frame.status === 'running' ? 'bg-yellow-50 text-yellow-700 border border-yellow-100' : 'bg-gray-50 text-gray-400'
        }`}>
          {frame.status === 'done' ? (hasHazard ? '🚨 存在隐患' : '🟢 安全') : frame.status === 'running' ? '🔄 研判中' : '⏳ 队列中'}
        </span>

        {/* Jump to video button */}
        {frame.status === 'done' && (
          <button
            onClick={(e) => {
              e.stopPropagation() // Avoid toggling expansion
              onSeek()
            }}
            className="ml-auto text-indigo-600 hover:text-indigo-800 hover:bg-indigo-50 font-medium px-2 py-1 rounded text-xs transition-colors flex items-center gap-1 shrink-0"
            title="在视频中查看此时间点"
          >
            ⏱️ 定位画面
          </button>
        )}

        {/* Expand icon */}
        {frame.status === 'done' && !!frame.report && (
          <span className={`text-gray-400 text-xs transition-transform duration-200 ml-1 ${expanded ? 'rotate-180' : ''}`}>
            ▼
          </span>
        )}
      </div>

      {/* Card Content (expanded report) */}
      {expanded && !!frame.report && (
        <div className="border-t border-gray-50 bg-gray-50/50 p-4">
          <InspectionReport report={frame.report as any} />
        </div>
      )}
    </div>
  )
}

// ── Main Component ─────────────────────────────────────────────────────────

interface Props {
  videoState: VideoInspectState
  onStop: () => void
  onSeekTo: (time: number) => void
}

function exportInspectionLog(frames: FrameEntry[]) {
  const completedFrames = frames.filter(f => f.status === 'done')
  const hazardFrames = completedFrames.filter(f => {
    const r = f.report as any
    const violations = r?.detection?.violations || []
    const overallLevel = r?.risk?.overall_level
    return (violations.length > 0) || (['critical', 'high', 'medium'].includes(overallLevel))
  })

  let md = `# 施工现场视频安全巡检日志\n\n`
  md += `**巡检时间**: ${new Date().toLocaleString()}\n`
  md += `**总分析帧数**: ${completedFrames.length} 帧\n`
  md += `**检测到安全隐患数**: ${hazardFrames.length} 处\n`
  md += `**安全比例**: ${completedFrames.length > 0 ? ((completedFrames.length - hazardFrames.length) / completedFrames.length * 100).toFixed(1) : 100}%\n\n`
  md += `## 🚨 安全隐患详细清单\n\n`

  if (hazardFrames.length === 0) {
    md += `🟢 本次巡检未发现任何违规安全隐患，现场状况良好。\n`
  } else {
    md += `| 时间点 | 整体风险等级 | 违规细节 / 风险摘要 | 处置决策 |\n`
    md += `| :--- | :--- | :--- | :--- |\n`
    hazardFrames.forEach(f => {
      const r = f.report as any
      const time = fmtTime(f.timestamp)
      const level = r?.risk?.overall_level === 'critical' ? '🔴 严重' : r?.risk?.overall_level === 'high' ? '🟠 高危' : '🟡 中危'
      const violations = r?.detection?.violations?.join(', ') || r?.risk?.summary || '安全隐患行为'
      const action = r?.decision?.action === 'immediate_stop' ? '🛑 立即停工' : r?.decision?.action === 'rectify' ? '⚠️ 限期整改' : '🔍 监控观察'
      md += `| ${time} | ${level} | ${violations} | ${action} |\n`
    })
  }

  md += `\n\n---\n*报告由 AI 哨兵多智能体安防系统自动生成。*`

  const blob = new Blob([md], { type: 'text/markdown;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `AI-Sentry-Inspect-Log-${new Date().toISOString().slice(0, 10)}.md`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

export function FrameInspectPanel({ videoState, onStop, onSeekTo }: Props) {
  const { frames, activeIdx, pendingIdx, isRunning } = videoState
  const [expandedIdx, setExpandedIdx] = useState<number | null>(null)
  const messagesEndRef = useRef<HTMLDivElement>(null)

  const runningFrame = activeIdx >= 0 ? frames.find(f => f.idx === activeIdx) : null
  const lastProcessedFrame = [...frames]
    .reverse()
    .find(f => f.status === 'done' || f.status === 'error' || f.status === 'running')
  const displayFrame = runningFrame || lastProcessedFrame

  const historyFrames = frames.filter(f => f.idx !== activeIdx && f.status !== 'queued')
  const completedFrames = frames.filter(f => f.status === 'done')
  const totalDone = completedFrames.length

  const hazardFrames = completedFrames.filter(f => {
    const r = f.report as any
    const violations = r?.detection?.violations || []
    const overallLevel = r?.risk?.overall_level
    return (violations.length > 0) || (['critical', 'high', 'medium'].includes(overallLevel))
  })

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [frames.length, activeIdx])

  return (
    <div className="flex-1 overflow-y-auto px-5 py-4 space-y-3">

      {/* Session status banner */}
      <div className="flex items-center gap-2 text-xs text-gray-500 bg-gray-50 border border-gray-200 rounded-xl px-3 py-2">
        <span className={isRunning ? 'text-green-500 animate-pulse' : 'text-gray-400'}>●</span>
        <span>
          {isRunning
            ? `视频巡检进行中 · 已完成 ${totalDone} 帧`
            : `视频巡检已结束 · 共完成 ${totalDone} 帧`}
        </span>
        {pendingIdx >= 0 && (
          <span className="ml-auto text-indigo-500">等待第 {pendingIdx + 1} 帧</span>
        )}
        {isRunning && (
          <button
            onClick={onStop}
            className="ml-auto text-red-500 hover:text-red-600 font-medium px-2 py-0.5 rounded-lg hover:bg-red-50 transition-colors"
          >
            停止
          </button>
        )}
      </div>

      {/* 🚧 Safety Inspection Overview Summary */}
      {completedFrames.length > 0 && (
        <div className="bg-white border border-gray-200 rounded-xl p-4 shadow-sm space-y-3">
          <div className="flex items-center justify-between gap-2 border-b border-gray-100 pb-2">
            <span className="text-sm font-bold text-gray-800">🚧 视频巡检概览 (Overview)</span>
            <button
              onClick={() => exportInspectionLog(frames)}
              className="text-xs bg-indigo-50 hover:bg-indigo-100 text-indigo-600 font-bold px-2.5 py-1.5 rounded-lg border border-indigo-100 transition-colors flex items-center gap-1"
            >
              📥 导出巡检日志
            </button>
          </div>
          <div className="grid grid-cols-3 gap-3 text-center">
            <div className="bg-gray-50/50 p-2.5 rounded-lg border border-gray-100/50">
              <p className="text-[10px] text-gray-400 font-medium leading-none mb-1">已分析帧数</p>
              <p className="text-lg font-mono font-bold text-gray-700">{completedFrames.length}</p>
            </div>
            <div className="bg-red-50/30 p-2.5 rounded-lg border border-red-100/30">
              <p className="text-[10px] text-red-500/80 font-medium leading-none mb-1">发现隐患数</p>
              <p className="text-lg font-mono font-bold text-red-600">{hazardFrames.length}</p>
            </div>
            <div className="bg-green-50/30 p-2.5 rounded-lg border border-green-100/30">
              <p className="text-[10px] text-green-600/80 font-medium leading-none mb-1">安全态占比</p>
              <p className="text-lg font-mono font-bold text-green-600">
                {completedFrames.length > 0
                  ? ((completedFrames.length - hazardFrames.length) / completedFrames.length * 100).toFixed(0)
                  : '100'}%
              </p>
            </div>
          </div>
          {hazardFrames.length > 0 && (
            <div className="space-y-1.5 pt-1">
              <p className="text-[10px] text-gray-400 font-semibold uppercase tracking-wider text-left">🚨 安全隐患时间线快捷跳转：</p>
              <div className="flex flex-wrap gap-1.5">
                {hazardFrames.map(f => {
                  const r = f.report as any
                  const isCritical = r?.risk?.overall_level === 'critical' || r?.risk?.overall_level === 'high'
                  return (
                    <button
                      key={f.idx}
                      onClick={() => {
                        onSeekTo(f.timestamp)
                        setExpandedIdx(f.idx) // Auto-expand this frame card
                      }}
                      className={`text-[10px] font-medium font-mono px-2 py-1 rounded-lg border flex items-center gap-1 transition-all hover:scale-105 active:scale-95 ${
                        isCritical
                          ? 'bg-red-50 hover:bg-red-100 text-red-600 border-red-200 shadow-sm shadow-red-100'
                          : 'bg-amber-50 hover:bg-amber-100 text-amber-600 border-amber-200'
                      }`}
                    >
                      <span className="w-1.5 h-1.5 rounded-full bg-current animate-pulse" />
                      <span>{fmtTime(f.timestamp)}</span>
                    </button>
                  )
                })}
              </div>
            </div>
          )}
        </div>
      )}

      {/* History (past completed frames) */}
      {historyFrames.length > 0 && (
        <div className="space-y-1.5">
          {historyFrames.map(f => (
            <HistoryFrameCard
              key={f.idx}
              frame={f}
              expanded={expandedIdx === f.idx}
              onToggle={() => setExpandedIdx(expandedIdx === f.idx ? null : f.idx)}
              onSeek={() => onSeekTo(f.timestamp)}
            />
          ))}
        </div>
      )}

      {/* Active frame or most recently completed frame */}
      {displayFrame ? (
        <ActiveFrameCard frame={displayFrame} />
      ) : isRunning && (
        <div className="flex gap-3 items-start">
          <div className="w-8 h-8 rounded-full bg-indigo-100 flex items-center justify-center text-base shrink-0">🤖</div>
          <div className="bg-white border border-gray-200 rounded-2xl rounded-tl-sm px-4 py-3 text-sm text-gray-400">
            等待第一帧…
          </div>
        </div>
      )}

      <div ref={messagesEndRef} />
    </div>
  )
}
