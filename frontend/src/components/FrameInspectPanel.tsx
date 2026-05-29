import { useEffect, useRef } from 'react'
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

  return (
    <div className="flex gap-3 items-start">
      <div className="w-8 h-8 rounded-full bg-indigo-100 flex items-center justify-center text-base shrink-0">🤖</div>
      <div className="flex-1 space-y-2 min-w-0">
        {/* Frame header */}
        <div className="bg-indigo-50 border border-indigo-200 rounded-2xl rounded-tl-sm px-4 py-2.5">
          <div className="flex items-center gap-2 text-xs font-medium text-indigo-700">
            <span>🎬 正在分析第 {frame.idx + 1} 帧</span>
            <span className="text-indigo-400">·</span>
            <span>{fmtTime(frame.timestamp)}</span>
            <span className="ml-auto text-indigo-400 animate-pulse">处理中…</span>
          </div>
        </div>

        {/* Thinking */}
        {frame.thinkingText && (
          <div className="bg-amber-50 border border-amber-200 rounded-2xl rounded-tl-sm px-4 py-2.5">
            <p className="text-xs text-amber-600 font-medium mb-1">💭 思考过程</p>
            <p className="text-xs text-amber-700 leading-relaxed whitespace-pre-wrap line-clamp-6">
              {frame.thinkingText}
            </p>
          </div>
        )}

        {/* Tool calls */}
        {frame.tools.length > 0 && (
          <div className="bg-white border border-gray-200 rounded-2xl rounded-tl-sm px-4 py-2.5">
            <p className="text-xs text-gray-400 font-medium mb-1.5">🔧 工具调用</p>
            {frame.tools.map((t, i) => <ToolRow key={i} tool={t} />)}
          </div>
        )}

        {/* Streaming output */}
        {frame.streamingText && (
          <div className="bg-white border border-gray-200 rounded-2xl rounded-tl-sm px-4 py-2.5">
            <p className="text-xs text-gray-400 font-medium mb-1.5">编排 Agent 输出</p>
            <pre className="text-xs text-gray-700 whitespace-pre-wrap font-sans leading-relaxed">
              {frame.streamingText}
              <span className="inline-block w-1.5 h-3.5 bg-indigo-500 ml-0.5 animate-pulse align-middle rounded-sm" />
            </pre>
          </div>
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}

function HistoryFrameCard({ frame }: { frame: FrameEntry }) {
  if (frame.status === 'dropped') return null
  return (
    <div className="flex items-center gap-2 px-3 py-2 rounded-xl bg-white border border-gray-100 text-xs">
      <span className={STATUS_COLOR[frame.status]}>{STATUS_ICON[frame.status]}</span>
      <span className="text-gray-500 font-mono">{fmtTime(frame.timestamp)}</span>
      <span className="text-gray-700">第 {frame.idx + 1} 帧</span>
      {frame.status === 'done' && !!frame.report && (
        <span className="ml-auto text-green-600 truncate max-w-[160px]">完成</span>
      )}
      {frame.status === 'error' && (
        <span className="ml-auto text-red-400">分析失败</span>
      )}
    </div>
  )
}

// ── Main Component ─────────────────────────────────────────────────────────

interface Props {
  videoState: VideoInspectState
  onStop: () => void
}

export function FrameInspectPanel({ videoState, onStop }: Props) {
  const { frames, activeIdx, pendingIdx, isRunning } = videoState
  const messagesEndRef = useRef<HTMLDivElement>(null)

  const activeFrame = activeIdx >= 0 ? frames.find(f => f.idx === activeIdx) : null
  const historyFrames = frames.filter(f => f.idx !== activeIdx && f.status !== 'queued')
  const totalDone = frames.filter(f => f.status === 'done').length

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

      {/* History (past completed frames) */}
      {historyFrames.length > 0 && (
        <div className="space-y-1.5">
          {historyFrames.map(f => <HistoryFrameCard key={f.idx} frame={f} />)}
        </div>
      )}

      {/* Active frame with streaming output */}
      {activeFrame && <ActiveFrameCard frame={activeFrame} />}

      {/* Idle state — waiting for next frame */}
      {!activeFrame && isRunning && (
        <div className="flex gap-3 items-start">
          <div className="w-8 h-8 rounded-full bg-indigo-100 flex items-center justify-center text-base shrink-0">🤖</div>
          <div className="bg-white border border-gray-200 rounded-2xl rounded-tl-sm px-4 py-3 text-sm text-gray-400">
            等待下一帧…
          </div>
        </div>
      )}

      <div ref={messagesEndRef} />
    </div>
  )
}
