import { useRef, useState, useEffect, useCallback } from 'react'
import type { FrameEntry } from '../hooks/useVideoInspect'

interface Props {
  src: string
  frames?: FrameEntry[]
  videoSeekTrigger?: { time: number; triggerId: number } | null
}

function fmt(s: number): string {
  if (!isFinite(s)) return '00:00'
  const m = Math.floor(s / 60)
  const sec = Math.floor(s % 60)
  return `${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`
}

const PlayIcon = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
    <path d="M4 2.5l10 5.5-10 5.5V2.5z" fill="currentColor"/>
  </svg>
)

const FullscreenIcon = () => (
  <svg width="15" height="15" viewBox="0 0 15 15" fill="none">
    <path d="M1 5V1h4M14 5V1h-4M1 10v4h4M14 10v4h-4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
  </svg>
)

const ExitFullscreenIcon = () => (
  <svg width="15" height="15" viewBox="0 0 15 15" fill="none">
    <path d="M5 1v4H1M10 1v4h4M5 14v-4H1M10 14v-4h4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
  </svg>
)

export function VideoPlayer({ src, frames, videoSeekTrigger }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const progressRef = useRef<HTMLDivElement>(null)
  const hideTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const [playing, setPlaying] = useState(false)
  const [currentTime, setCurrentTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [buffered, setBuffered] = useState(0)
  const [showControls, setShowControls] = useState(true)
  const [loading, setLoading] = useState(true)
  const [isFullscreen, setIsFullscreen] = useState(false)

  /* ── respond to parent videoSeekTrigger ── */
  useEffect(() => {
    console.log("VideoPlayer received videoSeekTrigger:", videoSeekTrigger);
    if (videoSeekTrigger && videoRef.current) {
      const v = videoRef.current;
      const targetTime = videoSeekTrigger.time;
      const seekAndPlay = () => {
        console.log("VideoPlayer executing seek to time:", targetTime, "video element current time was:", v.currentTime);
        v.currentTime = targetTime;
        v.play().catch((err) => {
          console.warn("VideoPlayer play error on seek:", err);
        });
        setPlaying(true);
      };

      if (v.readyState >= 1) {
        seekAndPlay();
      } else {
        console.log("VideoPlayer video element not ready, waiting for loadedmetadata event...");
        v.addEventListener('loadedmetadata', seekAndPlay, { once: true });
      }
    }
  }, [videoSeekTrigger])

  /* ── auto-play on src change ── */
  useEffect(() => {
    const v = videoRef.current
    if (!v) return
    setCurrentTime(0)
    setDuration(0)
    setBuffered(0)
    setPlaying(false)
    setLoading(true)
    v.load()
    const onReady = () => {
      v.play().catch(() => { /* blocked */ })
    }
    v.addEventListener('canplay', onReady, { once: true })
    return () => v.removeEventListener('canplay', onReady)
  }, [src])

  /* ── fullscreen change listener ── */
  useEffect(() => {
    const onChange = () => setIsFullscreen(!!document.fullscreenElement)
    document.addEventListener('fullscreenchange', onChange)
    return () => document.removeEventListener('fullscreenchange', onChange)
  }, [])

  /* ── controls auto-hide ── */
  const resetHideTimer = useCallback(() => {
    setShowControls(true)
    if (hideTimer.current) clearTimeout(hideTimer.current)
    hideTimer.current = setTimeout(() => setShowControls(false), 2500)
  }, [])

  useEffect(() => { resetHideTimer() }, [resetHideTimer])

  /* ── video event handlers ── */
  function onTimeUpdate() {
    const v = videoRef.current
    if (!v) return
    setCurrentTime(v.currentTime)
    if (v.buffered.length > 0) setBuffered(v.buffered.end(v.buffered.length - 1))
  }

  function onPlay() { setPlaying(true); setLoading(false) }
  function onPause() { setPlaying(false) }
  function onWaiting() { setLoading(true) }
  function onPlaying() { setLoading(false) }
  function onLoadedMetadata() {
    const v = videoRef.current
    if (v) setDuration(v.duration)
    setLoading(false)
  }

  /* ── controls actions ── */
  function togglePlay() {
    const v = videoRef.current
    if (!v) return
    if (v.paused) v.play()
    else v.pause()
    resetHideTimer()
  }

  function onSeekClick(e: React.MouseEvent<HTMLDivElement>) {
    const v = videoRef.current
    const bar = progressRef.current
    if (!v || !bar || !duration) return
    const rect = bar.getBoundingClientRect()
    const ratio = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width))
    v.currentTime = ratio * duration
    resetHideTimer()
  }

  function toggleFullscreen() {
    const el = containerRef.current
    if (!el) return
    if (!document.fullscreenElement) el.requestFullscreen()
    else document.exitFullscreen()
    resetHideTimer()
  }

  const progress = duration > 0 ? (currentTime / duration) * 100 : 0
  const bufferPct = duration > 0 ? (buffered / duration) * 100 : 0

  return (
    <div
      ref={containerRef}
      className="relative h-full w-full bg-black flex items-center justify-center group select-none"
      onMouseMove={resetHideTimer}
      onMouseLeave={() => { if (hideTimer.current) clearTimeout(hideTimer.current); setShowControls(false) }}
    >
      {/* Video element */}
      <video
        ref={videoRef}
        src={src}
        className="h-full w-full object-contain"
        onClick={togglePlay}
        onPlay={onPlay}
        onPause={onPause}
        onWaiting={onWaiting}
        onPlaying={onPlaying}
        onTimeUpdate={onTimeUpdate}
        onLoadedMetadata={onLoadedMetadata}
        onEnded={() => setPlaying(false)}
      />

      {/* Real-time AI HUD Warning Overlay */}
      {frames && frames.length > 0 && (() => {
        const windowIdx = Math.floor(currentTime / 5)
        const hudFrame = frames.find(f => f.idx === windowIdx)
        if (!hudFrame) return (
          <div className="absolute top-4 left-4 bg-black/60 backdrop-blur-md px-3 py-1.5 rounded-lg text-white/90 text-xs font-semibold flex items-center gap-2 border border-white/10 shadow-lg select-none">
            <span className="w-2 h-2 rounded-full bg-blue-500 animate-pulse" />
            <span>AI 哨兵实时安防监测中 · 待命</span>
          </div>
        )

        const report = hudFrame.report as any
        const overallLevel = report?.risk?.overall_level
        const violations = report?.detection?.violations || []
        const hasHazard = (violations.length > 0) || (['critical', 'high', 'medium'].includes(overallLevel))

        if (hudFrame.status === 'running') {
          return (
            <div className="absolute top-4 left-4 bg-black/60 backdrop-blur-md px-3 py-1.5 rounded-lg text-white/90 text-xs font-semibold flex items-center gap-2 border border-white/10 shadow-lg select-none">
              <span className="w-2 h-2 rounded-full bg-yellow-400 animate-ping" />
              <span>AI 哨兵正在研判第 {hudFrame.idx + 1} 帧 ({fmt(hudFrame.timestamp)})…</span>
            </div>
          )
        }

        if (hudFrame.status === 'done') {
          if (hasHazard) {
            return (
              <div className="absolute top-4 left-4 right-4 flex justify-between gap-4 pointer-events-none">
                <div className="bg-red-950/85 backdrop-blur-md px-3.5 py-2 rounded-xl text-red-100 text-xs font-semibold flex items-center gap-2.5 border border-red-500/30 shadow-[0_0_15px_rgba(239,68,68,0.3)] select-none">
                  <span className="w-2.5 h-2.5 rounded-full bg-red-500 animate-pulse shrink-0" />
                  <div className="flex flex-col gap-0.5 leading-tight text-left">
                    <span className="text-[10px] text-red-400 uppercase font-bold tracking-wider">🚨 AI 实时安全隐患警告 ({fmt(hudFrame.timestamp)})</span>
                    <span className="text-xs font-bold text-white">
                      {violations.length > 0 ? `检测到违规：${violations.join(', ')}` : (report?.risk?.summary || '发现安全隐患')}
                    </span>
                  </div>
                </div>
                {playing && (
                  <button
                    onClick={(e) => {
                      e.stopPropagation()
                      const v = videoRef.current
                      if (v) {
                        v.pause()
                        setPlaying(false)
                      }
                    }}
                    className="pointer-events-auto shrink-0 bg-red-600 hover:bg-red-500 text-white text-xs font-bold px-3 py-1.5 rounded-lg shadow-lg flex items-center gap-1 border border-red-500/40 transition-colors self-start"
                  >
                    ⏸ 暂停核对
                  </button>
                )}
              </div>
            )
          } else {
            return (
              <div className="absolute top-4 left-4 bg-black/60 backdrop-blur-md px-3 py-1.5 rounded-lg text-white/90 text-xs font-semibold flex items-center gap-2 border border-white/10 shadow-lg select-none">
                <span className="w-2.5 h-2.5 rounded-full bg-green-500 shrink-0" />
                <span>AI 哨兵实时安防监测中 · 安全 ({fmt(hudFrame.timestamp)})</span>
              </div>
            )
          }
        }

        return (
          <div className="absolute top-4 left-4 bg-black/60 backdrop-blur-md px-3 py-1.5 rounded-lg text-white/90 text-xs font-semibold flex items-center gap-2 border border-white/10 shadow-lg select-none">
            <span className="w-2 h-2 rounded-full bg-gray-500 animate-pulse" />
            <span>AI 哨兵安全监测中</span>
          </div>
        )
      })()}

      {/* Loading spinner */}
      {loading && (
        <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
          <div className="w-10 h-10 rounded-full border-2 border-white/20 border-t-white/80 animate-spin" />
        </div>
      )}

      {/* Centre play overlay (shown when paused & controls visible) */}
      {!playing && !loading && showControls && (
        <div
          className="absolute inset-0 flex items-center justify-center pointer-events-none"
        >
          <div className="w-16 h-16 rounded-full bg-black/40 backdrop-blur-sm flex items-center justify-center text-white">
            <PlayIcon />
          </div>
        </div>
      )}

      {/* Controls bar */}
      <div
        className={`absolute bottom-0 left-0 right-0 bg-gradient-to-t from-black/80 via-black/40 to-transparent pt-8 pb-2 px-3 transition-opacity duration-300 ${showControls ? 'opacity-100' : 'opacity-0 pointer-events-none'}`}
      >
        {/* Progress bar */}
        <div
          ref={progressRef}
          className="relative h-1.5 rounded-full bg-white/20 cursor-pointer mb-3 group/seek"
          onClick={onSeekClick}
        >
          {/* Buffer */}
          <div className="absolute left-0 top-0 h-full rounded-full bg-white/30 transition-all" style={{ width: `${bufferPct}%` }} />
          {/* Played */}
          <div className="absolute left-0 top-0 h-full rounded-full bg-indigo-400 transition-all" style={{ width: `${progress}%` }} />
          {/* Thumb */}
          <div
            className="absolute top-1/2 -translate-y-1/2 w-3 h-3 rounded-full bg-white shadow opacity-0 group-hover/seek:opacity-100 transition-opacity pointer-events-none"
            style={{ left: `calc(${progress}% - 6px)` }}
          />

          {/* Timeline Risk Markers */}
          {duration > 0 && frames && frames.map((frame) => {
            const pct = (frame.timestamp / duration) * 100
            const report = frame.report as any
            const overallLevel = report?.risk?.overall_level
            const violations = report?.detection?.violations || []
            const hasHazard = (violations.length > 0) || (['critical', 'high', 'medium'].includes(overallLevel))

            let dotColor = 'bg-gray-400 border-gray-300' // queued / other
            if (frame.status === 'running') {
              dotColor = 'bg-indigo-400 animate-pulse scale-110 border-indigo-300'
            } else if (frame.status === 'done') {
              dotColor = hasHazard ? 'bg-red-500 shadow-[0_0_8px_rgba(239,68,68,0.9)] border-red-400' : 'bg-green-500 border-green-400'
            } else if (frame.status === 'error') {
              dotColor = 'bg-red-300 border-red-400'
            }

            return (
              <div
                key={frame.idx}
                className={`absolute top-1/2 -translate-y-1/2 -translate-x-1/2 w-2 h-2 rounded-full border border-black/40 z-20 cursor-pointer transition-all hover:scale-150 group/marker ${dotColor}`}
                style={{ left: `${pct}%` }}
                onClick={(e) => {
                  e.stopPropagation() // Avoid triggering parent onSeekClick
                  const v = videoRef.current
                  if (v) {
                    v.currentTime = frame.timestamp
                    resetHideTimer()
                  }
                }}
              >
                {/* Hover Tooltip */}
                <div className="absolute bottom-4 left-1/2 -translate-x-1/2 bg-black/95 text-white text-[10px] p-2.5 rounded-xl whitespace-nowrap opacity-0 pointer-events-none group-hover/marker:opacity-100 transition-opacity duration-200 z-50 shadow-xl border border-white/10 select-none leading-relaxed flex flex-col gap-0.5">
                  <div className="flex items-center gap-1.5 font-bold">
                    <span>⏱️ {fmt(frame.timestamp)}</span>
                    <span>·</span>
                    <span>第 {frame.idx + 1} 帧</span>
                    <span className={`px-1 rounded text-[9px] ${
                      frame.status === 'done'
                        ? hasHazard ? 'bg-red-500/20 text-red-400 border border-red-500/30' : 'bg-green-500/20 text-green-400 border border-green-500/30'
                        : frame.status === 'running' ? 'bg-indigo-500/20 text-indigo-400 border border-indigo-500/30' : 'bg-gray-500/20 text-gray-400'
                    }`}>
                      {frame.status === 'done' ? (hasHazard ? '🚨 存在隐患' : '🟢 正常') : frame.status === 'running' ? '🔄 巡检中' : '⏳ 等待'}
                    </span>
                  </div>
                  {frame.status === 'done' && (
                    <div className="text-gray-300 max-w-[200px] truncate-2-lines text-[9px] mt-0.5 text-left whitespace-normal leading-normal">
                      {violations.length > 0 ? `违规: ${violations.join(', ')}` : (report?.risk?.summary || '安全，未检测到显著异常行为')}
                    </div>
                  )}
                </div>
              </div>
            )
          })}
        </div>

        {/* Controls row */}
        <div className="flex items-center gap-3 text-white/90">
          {/* Time */}
          <span className="text-xs font-mono text-white/70 tabular-nums shrink-0">
            {fmt(currentTime)} / {fmt(duration)}
          </span>

          <div className="flex-1" />

          {/* Fullscreen */}
          <button onClick={toggleFullscreen} className="hover:text-white transition-colors shrink-0">
            {isFullscreen ? <ExitFullscreenIcon /> : <FullscreenIcon />}
          </button>
        </div>
      </div>
    </div>
  )
}
