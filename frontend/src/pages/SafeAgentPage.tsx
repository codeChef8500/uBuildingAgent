import { useState, useRef, useEffect } from 'react'
import type { ReactNode } from 'react'
import { MediaPanel } from '../components/MediaPanel'
import { SceneInputForm } from '../components/SceneInputForm'
import { InspectionReport } from '../components/InspectionReport'
import { FrameInspectPanel } from '../components/FrameInspectPanel'
import { useInspect } from '../hooks/useInspect'
import { useVideoInspect } from '../hooks/useVideoInspect'

/* ── Bubble components ───────────────────────────────────────── */

function WelcomeBubble() {
  return (
    <div className="flex gap-3 items-start">
      <div className="w-8 h-8 rounded-full bg-indigo-100 flex items-center justify-center text-base shrink-0">🛡️</div>
      <div className="bg-white border border-gray-200 rounded-2xl rounded-tl-sm px-4 py-3 max-w-sm shadow-sm">
        <p className="text-sm font-medium text-gray-800 mb-0.5">AI 哨兵就绪</p>
        <p className="text-xs text-gray-400 leading-relaxed">
          在左侧上传施工现场图片或视频，<br />
          填写场景描述后发送，即可启动多智能体安全巡检分析。
        </p>
      </div>
    </div>
  )
}

function UserBubble({ text, location }: { text: string; location?: string }) {
  return (
    <div className="flex justify-end gap-3 items-start">
      <div className="bg-indigo-600 text-white rounded-2xl rounded-tr-sm px-4 py-3 max-w-sm shadow-sm">
        {location && <p className="text-xs text-indigo-200 mb-1">📍 {location}</p>}
        <p className="text-sm leading-relaxed">{text}</p>
      </div>
      <div className="w-8 h-8 rounded-full bg-gray-200 flex items-center justify-center text-xs text-gray-500 shrink-0 font-medium">我</div>
    </div>
  )
}

function AgentBubble({ children }: { children: ReactNode }) {
  return (
    <div className="flex gap-3 items-start">
      <div className="w-8 h-8 rounded-full bg-indigo-100 flex items-center justify-center text-base shrink-0">🤖</div>
      <div className="flex-1 max-w-xl space-y-2">{children}</div>
    </div>
  )
}

function StreamingBubble({ text, running }: { text: string; running: boolean }) {
  const bottomRef = useRef<HTMLDivElement>(null)
  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [text])
  if (!text && !running) return null
  return (
    <AgentBubble>
      <div className="bg-white border border-gray-200 rounded-2xl rounded-tl-sm px-4 py-3 shadow-sm">
        <p className="text-xs text-gray-400 mb-2 font-medium">编排智能体输出</p>
        <pre className="text-sm text-gray-700 whitespace-pre-wrap font-sans leading-relaxed">
          {text}
          {running && <span className="inline-block w-1.5 h-4 bg-indigo-500 ml-0.5 animate-pulse align-middle rounded-sm" />}
        </pre>
        <div ref={bottomRef} />
      </div>
    </AgentBubble>
  )
}

/* ── Main Page ──────────────────────────────────────────────── */

export function SafeAgentPage() {
  // ── Normal single-image/text inspection ───────────────────────
  const { state, start, reset } = useInspect()
  const [mediaUrl, setMediaUrl] = useState('')
  const [mediaType, setMediaType] = useState<'image' | 'video' | 'camera' | 'none'>('none')
  const [mediaName, setMediaName] = useState('')
  const [submitted, setSubmitted] = useState<{ description: string; location: string } | null>(null)
  const [videoSeekTrigger, setVideoSeekTrigger] = useState<{ time: number; triggerId: number } | null>(null)
  const chatBottomRef = useRef<HTMLDivElement>(null)

  const running = state.status === 'running'
  const isDone = state.status === 'done'

  useEffect(() => {
    chatBottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [state.streamingText, state.report])

  function handleMediaChange(url: string, type: 'image' | 'video' | 'camera' | 'none', name?: string) {
    setMediaUrl(url)
    setMediaType(type)
    setMediaName(name || '')
    setVideoSeekTrigger(null)
  }

  function handleStart(description: string, url: string, location: string) {
    setSubmitted({ description, location })
    start(description, url, location)
  }

  function handleReset() {
    reset()
    setSubmitted(null)
  }

  // ── Video frame inspection ─────────────────────────────────────
  const { state: videoState, start: videoStart, stop: videoStop } = useVideoInspect()

  // ── Auto-trigger video inspection ────────────────────────────────
  const lastTriggeredUrlRef = useRef<string | null>(null)

  useEffect(() => {
    if (mediaType === 'video' && mediaUrl) {
      if (lastTriggeredUrlRef.current !== mediaUrl) {
        lastTriggeredUrlRef.current = mediaUrl
        const desc = mediaName ? `视频安全巡检: ${mediaName}` : '视频自动安全巡检'
        videoStart(mediaUrl, desc)
      }
    } else {
      lastTriggeredUrlRef.current = null
    }
  }, [mediaUrl, mediaType, mediaName, videoStart])

  const isVideoMode = mediaType === 'video'

  // ── Render ─────────────────────────────────────────────────────
  return (
    <div className="flex h-screen overflow-hidden">
      {/* ===== LEFT: full-screen media player ===== */}
      <div className="w-3/5 shrink-0 relative">
        <MediaPanel
          onMediaChange={handleMediaChange}
          frames={isVideoMode ? videoState.frames : undefined}
          videoSeekTrigger={videoSeekTrigger}
        />
      </div>

      {/* ===== RIGHT: context-aware panel ===== */}
      <div className="flex-1 flex flex-col bg-gray-50 overflow-hidden">

        {/* ── VIDEO MODE ── */}
        {isVideoMode ? (
          <>
            {/* Video header */}
            <div className="px-5 py-2.5 bg-white border-b border-gray-100 shrink-0 flex items-center justify-between gap-2">
              <div className="flex items-center gap-2">
                <span className="text-xs font-medium text-indigo-700">🎬 视频逐帧巡检</span>
                <span className="text-gray-300">·</span>
                <span className="text-xs text-gray-400">每 5 秒提取一帧，滑动窗口调度</span>
              </div>
              {videoState.isRunning && (
                <button
                  onClick={videoStop}
                  className="px-3 py-1 bg-red-100 hover:bg-red-200 text-red-600 text-xs font-medium rounded-lg transition-colors flex items-center gap-1 shadow-sm"
                >
                  🛑 停止巡检
                </button>
              )}
            </div>

            {/* Frame inspection panel (scrollable) */}
            {videoState.frames.length > 0 || videoState.isRunning ? (
              <FrameInspectPanel
                videoState={videoState}
                onStop={videoStop}
                onSeekTo={(time) => {
                  console.log("SafeAgentPage onSeekTo clicked for time:", time);
                  setVideoSeekTrigger({ time, triggerId: Date.now() });
                }}
              />
            ) : (
              <div className="flex-1 flex flex-col items-center justify-center px-8 text-center gap-3">
                <div className="text-5xl animate-bounce">🤖</div>
                <p className="text-sm font-medium text-gray-700">等待上传视频</p>
                <p className="text-xs text-gray-400 leading-relaxed">
                  请在左侧上传施工现场视频或输入视频 URL<br />
                  上传后系统将自动启动 AI 逐帧巡检
                </p>
              </div>
            )}
          </>
        ) : (
          /* ── NORMAL CHAT MODE ── */
          <>
            {isDone && (
              <div className="px-5 py-2 bg-white border-b border-gray-100 shrink-0 flex justify-end">
                <button
                  onClick={handleReset}
                  className="text-xs text-indigo-600 hover:text-indigo-500 font-medium px-3 py-1.5 rounded-lg hover:bg-indigo-50 transition-colors"
                >
                  新建巡检
                </button>
              </div>
            )}

            <div className="flex-1 overflow-y-auto px-5 py-4 space-y-4">
              <WelcomeBubble />

              {submitted && (
                <UserBubble text={submitted.description} location={submitted.location || undefined} />
              )}

              {state.status === 'error' && (
                <AgentBubble>
                  <div className="bg-red-50 border border-red-200 text-red-700 text-sm rounded-2xl rounded-tl-sm px-4 py-3">
                    ⚠️ 巡检失败：{state.errorMsg}
                  </div>
                </AgentBubble>
              )}

              <StreamingBubble text={state.streamingText} running={running} />

              {state.report && (
                <AgentBubble>
                  <InspectionReport report={state.report} />
                </AgentBubble>
              )}

              <div ref={chatBottomRef} />
            </div>

            <div className="px-4 py-3 bg-white border-t border-gray-100 shrink-0">
              <SceneInputForm
                imageUrl={mediaUrl}
                onStart={handleStart}
                onReset={handleReset}
                running={running}
              />
            </div>
          </>
        )}
      </div>
    </div>
  )
}
