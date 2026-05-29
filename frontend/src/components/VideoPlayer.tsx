import { useRef, useState, useEffect, useCallback } from 'react'

interface Props {
  src: string
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

const PauseIcon = () => (
  <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
    <rect x="3" y="2" width="4" height="12" rx="1.5" fill="currentColor"/>
    <rect x="9" y="2" width="4" height="12" rx="1.5" fill="currentColor"/>
  </svg>
)

const VolumeIcon = ({ muted, volume }: { muted: boolean; volume: number }) => {
  if (muted || volume === 0) return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <path d="M2 6h3l4-3v10L5 10H2V6z" fill="currentColor"/>
      <path d="M11 6l3 3m0-3l-3 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/>
    </svg>
  )
  if (volume < 0.5) return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <path d="M2 6h3l4-3v10L5 10H2V6z" fill="currentColor"/>
      <path d="M11 5.5a3 3 0 010 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" fill="none"/>
    </svg>
  )
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <path d="M2 6h3l4-3v10L5 10H2V6z" fill="currentColor"/>
      <path d="M11 5a3.5 3.5 0 010 6M13 3.5a6 6 0 010 9" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" fill="none"/>
    </svg>
  )
}

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

export function VideoPlayer({ src }: Props) {
  const videoRef = useRef<HTMLVideoElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const progressRef = useRef<HTMLDivElement>(null)
  const hideTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const [playing, setPlaying] = useState(false)
  const [currentTime, setCurrentTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [buffered, setBuffered] = useState(0)
  const [volume, setVolume] = useState(1)
  const [muted, setMuted] = useState(false)
  const [showControls, setShowControls] = useState(true)
  const [showVolume, setShowVolume] = useState(false)
  const [loading, setLoading] = useState(true)
  const [isFullscreen, setIsFullscreen] = useState(false)

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

  function toggleMute() {
    const v = videoRef.current
    if (!v) return
    v.muted = !v.muted
    setMuted(v.muted)
  }

  function onVolumeChange(e: React.ChangeEvent<HTMLInputElement>) {
    const v = videoRef.current
    if (!v) return
    const val = parseFloat(e.target.value)
    v.volume = val
    setVolume(val)
    if (val === 0) { v.muted = true; setMuted(true) }
    else { v.muted = false; setMuted(false) }
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
      onMouseLeave={() => { if (hideTimer.current) clearTimeout(hideTimer.current); setShowControls(false); setShowVolume(false) }}
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
          className="relative h-1 rounded-full bg-white/20 cursor-pointer mb-3 group/seek"
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
        </div>

        {/* Controls row */}
        <div className="flex items-center gap-3 text-white/90">
          {/* Play/Pause */}
          <button onClick={togglePlay} className="hover:text-white transition-colors shrink-0">
            {playing ? <PauseIcon /> : <PlayIcon />}
          </button>

          {/* Volume */}
          <div
            className="flex items-center gap-2 shrink-0"
            onMouseEnter={() => setShowVolume(true)}
            onMouseLeave={() => setShowVolume(false)}
          >
            <button onClick={toggleMute} className="hover:text-white transition-colors">
              <VolumeIcon muted={muted} volume={volume} />
            </button>
            <div className={`overflow-hidden transition-all duration-200 ${showVolume ? 'w-16 opacity-100' : 'w-0 opacity-0'}`}>
              <input
                type="range"
                min={0} max={1} step={0.05}
                value={muted ? 0 : volume}
                onChange={onVolumeChange}
                className="w-16 h-1 accent-indigo-400 cursor-pointer"
              />
            </div>
          </div>

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
