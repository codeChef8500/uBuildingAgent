import { useState, useRef, useCallback } from 'react'
// @ts-ignore
import * as MP4Box from 'mp4box'

// ── Types ──────────────────────────────────────────────────────────────────

export type VideoEventType =
  | 'frame_start' | 'frame_drop' | 'frame_done' | 'queue_status'
  | 'text_delta'  | 'thinking'   | 'tool_start'  | 'tool_end'
  | 'error'       | 'session_end'

export interface VideoEvent {
  type: VideoEventType
  frame_idx: number
  timestamp: number
  delta?: string
  tool_name?: string
  tool_args?: string
  tool_result?: string
  report?: unknown
  err?: string
  running_idx: number  // -1 = none
  pending_idx: number  // -1 = none
}

export interface ToolActivity {
  name: string
  args: string
  result: string
  done: boolean
}

export type FrameStatus = 'queued' | 'running' | 'done' | 'dropped' | 'error'

export interface FrameEntry {
  idx: number
  timestamp: number   // seconds into the video
  status: FrameStatus
  streamingText: string
  thinkingText: string
  tools: ToolActivity[]
  report: unknown | null
}

export interface VideoInspectState {
  sessionId: string | null
  isRunning: boolean
  frames: FrameEntry[]
  activeIdx: number    // idx of the frame currently processing (-1 = none)
  pendingIdx: number   // idx of the frame waiting in queue (-1 = none)
}

// ── WebCodecs Frame Extractor ──────────────────────────────────────────────

async function extractFramesWebCodecs(
  mediaUrl: string,
  targetTimes: number[]
): Promise<Map<number, Blob>> {
  const framesMap = new Map<number, Blob>()

  if (typeof VideoDecoder === 'undefined' || typeof EncodedVideoChunk === 'undefined') {
    throw new Error('WebCodecs is not supported in this browser')
  }

  const response = await fetch(mediaUrl)
  const buffer = await response.arrayBuffer()
  // @ts-ignore
  buffer.fileStart = 0

  const mp4boxfile = MP4Box.createFile()

  return new Promise((resolve, reject) => {
    let videoTrack: any = null
    let trackId = 0
    let decoder: VideoDecoder | null = null

    mp4boxfile.onError = (e: any) => {
      reject(new Error(String(e)))
    }

    mp4boxfile.onReady = (info: any) => {
      videoTrack = info.tracks.find((t: any) => t.video)
      if (!videoTrack) {
        reject(new Error('No video track found in MP4'))
        return
      }
      trackId = videoTrack.id
      mp4boxfile.setExtractionOptions(trackId, null, { nbSamples: videoTrack.nb_samples })
      mp4boxfile.start()
    }

    mp4boxfile.onSamples = async (id: any, _ref: any, samples: any[]) => {
      try {
        const track = mp4boxfile.getTrackById(id)
        
        let description: Uint8Array | undefined
        for (const entry of (track.mdia.minf.stbl.stsd.entries as any[])) {
          const entryAny = entry as any
          const box = entryAny.avcC || entryAny.hvcC
          if (box) {
            const stream = new (MP4Box as any).DataStream(undefined, 0, (MP4Box as any).DataStream.BIG_ENDIAN)
            box.write(stream)
            description = new Uint8Array(stream.buffer, 8) // skip box size & type
            break
          }
        }

        decoder = new VideoDecoder({
          output: async (frame: VideoFrame) => {
            const frameTimeS = frame.timestamp / 1000000
            // Find closest target time within 0.5s tolerance
            const matchedTime = targetTimes.find(t => Math.abs(t - frameTimeS) < 0.5)
            if (matchedTime !== undefined && !framesMap.has(matchedTime)) {
              const canvas = new OffscreenCanvas(frame.displayWidth, frame.displayHeight)
              const ctx = canvas.getContext('2d')
              if (ctx) {
                ctx.drawImage(frame, 0, 0)
                const blob = await canvas.convertToBlob({ type: 'image/jpeg', quality: 0.7 })
                framesMap.set(matchedTime, blob)
              }
            }
            frame.close()
          },
          error: (e) => {
            console.error('VideoDecoder error:', e)
          }
        })

        decoder.configure({
          codec: videoTrack.codec,
          codedWidth: videoTrack.video.width,
          codedHeight: videoTrack.video.height,
          description: description,
        })

        for (const sample of samples) {
          const chunk = new EncodedVideoChunk({
            type: sample.is_sync ? 'key' : 'delta',
            timestamp: (sample.cts * 1000000) / sample.timescale,
            duration: (sample.duration * 1000000) / sample.timescale,
            data: sample.data,
          })
          decoder.decode(chunk)
        }

        await decoder.flush()
        decoder.close()
        resolve(framesMap)
      } catch (err) {
        reject(err)
      }
    }

    mp4boxfile.appendBuffer(buffer as any)
    mp4boxfile.flush()
  })
}

// ── Constants ──────────────────────────────────────────────────────────────

const FRAME_INTERVAL_S = 2   // extract one frame every N seconds of video time
const REAL_TIME_DELAY_MS = 2000  // send one frame every N real-time milliseconds
const MAX_FRAMES = 300        // safety limit (10-min video at 2s/frame)

// ── Hook ───────────────────────────────────────────────────────────────────

export function useVideoInspect() {
  const [state, setState] = useState<VideoInspectState>({
    sessionId: null,
    isRunning: false,
    frames: [],
    activeIdx: -1,
    pendingIdx: -1,
  })

  const isRunningRef = useRef(false)
  const sessionIdRef = useRef<string | null>(null)
  const sseRef = useRef<EventSource | null>(null)
  const frameTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // ── Event processor ───────────────────────────────────────────────────

  const processEvent = useCallback((ev: VideoEvent) => {
    setState(prev => {
      const frames = [...prev.frames]

      const ensureFrame = (idx: number, timestamp: number): FrameEntry => {
        let f = frames.find(x => x.idx === idx)
        if (!f) {
          f = { idx, timestamp, status: 'queued', streamingText: '', thinkingText: '', tools: [], report: null }
          frames.push(f)
          frames.sort((a, b) => a.idx - b.idx)
        }
        return f
      }

      const updateFrame = (idx: number, updater: (f: FrameEntry) => FrameEntry) => {
        const i = frames.findIndex(x => x.idx === idx)
        if (i >= 0) frames[i] = updater({ ...frames[i] })
      }

      switch (ev.type) {
        case 'frame_start': {
          const f = ensureFrame(ev.frame_idx, ev.timestamp)
          f.status = 'running'
          break
        }
        case 'frame_drop':
          updateFrame(ev.frame_idx, f => ({ ...f, status: 'dropped' }))
          break

        case 'text_delta':
          updateFrame(ev.frame_idx, f => ({
            ...f,
            streamingText: f.streamingText + (ev.delta ?? ''),
          }))
          break

        case 'thinking':
          updateFrame(ev.frame_idx, f => ({
            ...f,
            thinkingText: f.thinkingText + (ev.delta ?? ''),
          }))
          break

        case 'tool_start':
          updateFrame(ev.frame_idx, f => ({
            ...f,
            tools: [...f.tools, { name: ev.tool_name ?? '', args: ev.tool_args ?? '', result: '', done: false }],
          }))
          break

        case 'tool_end':
          updateFrame(ev.frame_idx, f => {
            const tools = [...f.tools]
            // Mark the last matching tool as done
            for (let i = tools.length - 1; i >= 0; i--) {
              if (tools[i].name === ev.tool_name && !tools[i].done) {
                tools[i] = { ...tools[i], result: ev.tool_result ?? '', done: true }
                break
              }
            }
            return { ...f, tools }
          })
          break

        case 'frame_done':
          updateFrame(ev.frame_idx, f => ({
            ...f,
            status: 'done',
            report: ev.report ?? null,
          }))
          break

        case 'error':
          updateFrame(ev.frame_idx, f => ({ ...f, status: 'error' }))
          break
      }

      return {
        ...prev,
        frames,
        activeIdx: ev.running_idx,
        pendingIdx: ev.pending_idx,
      }
    })
  }, [])

  // ── Frame extraction ──────────────────────────────────────────────────

  const captureFrame = useCallback((videoEl: HTMLVideoElement, timeS: number): Promise<Blob | null> => {
    return new Promise(resolve => {
      const onSeeked = () => {
        const canvas = document.createElement('canvas')
        canvas.width = videoEl.videoWidth || 640
        canvas.height = videoEl.videoHeight || 360
        const ctx = canvas.getContext('2d')
        if (!ctx) { resolve(null); return }
        ctx.drawImage(videoEl, 0, 0)
        canvas.toBlob(blob => resolve(blob), 'image/jpeg', 0.7)
      }
      videoEl.addEventListener('seeked', onSeeked, { once: true })
      videoEl.currentTime = timeS
    })
  }, [])

  const submitFrame = useCallback(async (
    sessionId: string,
    idx: number,
    timestamp: number,
    description: string,
    blob: Blob,
  ) => {
    const fd = new FormData()
    fd.append('session_id', sessionId)
    fd.append('frame_idx', String(idx))
    fd.append('timestamp', String(timestamp))
    fd.append('description', description)
    fd.append('frame', blob, `frame-${idx}.jpg`)

    await fetch('/api/safeagent/video/frame', { method: 'POST', body: fd })
      .catch(e => console.warn('submit frame error', e))
  }, [])

  // ── Start / Stop ──────────────────────────────────────────────────────

  const start = useCallback(async (mediaUrl: string, description: string) => {
    if (isRunningRef.current) return

    // Create session
    const res = await fetch('/api/safeagent/video/start', { method: 'POST' })
    if (!res.ok) return
    const { session_id: sid } = await res.json()
    sessionIdRef.current = sid
    isRunningRef.current = true

    setState({
      sessionId: sid,
      isRunning: true,
      frames: [],
      activeIdx: -1,
      pendingIdx: -1,
    })

    // Open SSE stream
    const sse = new EventSource(`/api/safeagent/video/events?session_id=${sid}`)
    sseRef.current = sse
    sse.onmessage = (e) => {
      const ev: VideoEvent = JSON.parse(e.data)
      processEvent(ev)
      if (ev.type === 'session_end') {
        cleanup()
      }
    }
    sse.onerror = () => cleanup()

    // Create a hidden video element for frame capture (doesn't affect the visible player)
    const vid = document.createElement('video')
    vid.src = mediaUrl
    vid.crossOrigin = 'anonymous'
    vid.preload = 'auto'
    vid.muted = true

    await new Promise<void>(resolve => {
      vid.addEventListener('loadedmetadata', () => resolve(), { once: true })
      vid.load()
    })

    const duration = isFinite(vid.duration) ? vid.duration : 0
    let frameIdx = 0
    let currentTime = 0

    // Construct target extraction times for WebCodecs
    const targetTimes: number[] = []
    let t = 0
    let idx = 0
    while (t <= duration && idx < MAX_FRAMES) {
      targetTimes.push(t)
      t += FRAME_INTERVAL_S
      idx++
    }

    // Try WebCodecs pre-decoding (Strategy A)
    let preDecodedFrames: Map<number, Blob> | null = null
    try {
      console.log('SafeAgent: Attempting WebCodecs fast frame decoding...')
      preDecodedFrames = await extractFramesWebCodecs(mediaUrl, targetTimes)
      console.log('SafeAgent: WebCodecs frame decoding successful, frames extracted:', preDecodedFrames?.size)
    } catch (err) {
      console.warn('SafeAgent: WebCodecs fast decoding failed, falling back to sequential Seeking:', err)
    }

    const scheduleNext = () => {
      if (!isRunningRef.current) return
      if (currentTime > duration || frameIdx >= MAX_FRAMES) {
        return
      }

      frameTimerRef.current = setTimeout(async () => {
        if (!isRunningRef.current) return

        let blob: Blob | null = null
        if (preDecodedFrames && preDecodedFrames.has(currentTime)) {
          blob = preDecodedFrames.get(currentTime) || null
        } else {
          blob = await captureFrame(vid, currentTime)
        }

        const capturedSid = sessionIdRef.current
        if (blob && capturedSid && isRunningRef.current) {
          // Register frame in local state first
          setState(prev => ({
            ...prev,
            frames: [
              ...prev.frames,
              { idx: frameIdx, timestamp: currentTime, status: 'queued', streamingText: '', thinkingText: '', tools: [], report: null },
            ],
          }))
          submitFrame(capturedSid, frameIdx, currentTime, description, blob)
        }
        frameIdx++
        currentTime += FRAME_INTERVAL_S
        scheduleNext()
      }, REAL_TIME_DELAY_MS)
    }

    // Extract first frame immediately (no delay for the very first one)
    let firstBlob: Blob | null = null
    if (preDecodedFrames && preDecodedFrames.has(0)) {
      firstBlob = preDecodedFrames.get(0) || null
    } else {
      firstBlob = await captureFrame(vid, 0)
    }

    if (firstBlob && sid && isRunningRef.current) {
      setState(prev => ({
        ...prev,
        frames: [{ idx: 0, timestamp: 0, status: 'queued', streamingText: '', thinkingText: '', tools: [], report: null }],
      }))
      submitFrame(sid, 0, 0, description, firstBlob)
    }
    frameIdx = 1
    currentTime = FRAME_INTERVAL_S
    scheduleNext()
  }, [captureFrame, submitFrame, processEvent])

  const cleanup = useCallback(() => {
    isRunningRef.current = false
    if (frameTimerRef.current) {
      clearTimeout(frameTimerRef.current)
      frameTimerRef.current = null
    }
    if (sseRef.current) {
      sseRef.current.close()
      sseRef.current = null
    }
    setState(prev => ({ ...prev, isRunning: false }))
  }, [])

  const stop = useCallback(async () => {
    const sid = sessionIdRef.current
    cleanup()
    if (sid) {
      await fetch('/api/safeagent/video/stop', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: sid }),
      }).catch(() => {})
      sessionIdRef.current = null
    }
    setState(prev => ({ ...prev, sessionId: null, isRunning: false }))
  }, [cleanup])

  return { state, start, stop }
}
