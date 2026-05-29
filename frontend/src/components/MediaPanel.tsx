import { useRef, useState } from 'react'
import { VideoPlayer } from './VideoPlayer'

interface MediaItem {
  url: string
  type: 'image' | 'video' | 'camera'
  name: string
}

type InputMode = 'none' | 'url' | 'camera'

interface Props {
  onMediaChange: (url: string, type: 'image' | 'video' | 'camera' | 'none') => void
}

export function MediaPanel({ onMediaChange }: Props) {
  const [items, setItems] = useState<MediaItem[]>([])
  const [active, setActive] = useState(0)
  const [urlInput, setUrlInput] = useState('')
  const [rtspInput, setRtspInput] = useState('')
  const [inputMode, setInputMode] = useState<InputMode>('none')
  const fileRef = useRef<HTMLInputElement>(null)

  function selectItem(idx: number, url: string, type: MediaItem['type']) {
    setActive(idx)
    onMediaChange(url, type)
  }

  function toggleMode(mode: InputMode) {
    setInputMode((v) => (v === mode ? 'none' : mode))
  }

  function addFromUrl() {
    if (!urlInput.trim()) return
    const isVideo = /\.(mp4|webm|ogg|mov)$/i.test(urlInput)
    const item: MediaItem = { url: urlInput.trim(), type: isVideo ? 'video' : 'image', name: urlInput.trim() }
    const next = [...items, item]
    setItems(next)
    selectItem(next.length - 1, item.url, item.type)
    setUrlInput('')
    setInputMode('none')
  }

  function connectCamera() {
    const raw = rtspInput.trim()
    if (!raw) return
    const proxyUrl = `/api/camera/stream?url=${encodeURIComponent(raw)}`
    const item: MediaItem = { url: proxyUrl, type: 'camera', name: raw }
    const next = [...items, item]
    setItems(next)
    selectItem(next.length - 1, proxyUrl, 'camera')
    setRtspInput('')
    setInputMode('none')
  }

  function handleFiles(files: FileList | null) {
    if (!files) return
    const newItems: MediaItem[] = Array.from(files).map((f) => ({
      url: URL.createObjectURL(f),
      type: f.type.startsWith('video/') ? 'video' : 'image',
      name: f.name,
    }))
    const next = [...items, ...newItems]
    setItems(next)
    const first = next[next.length - newItems.length]
    selectItem(next.length - newItems.length, first.url, first.type)
  }

  const current = items[active] ?? null

  return (
    <div className="relative h-full bg-black overflow-hidden flex flex-col">
      {/* Main viewer */}
      <div className="flex-1 min-h-0 relative">
        {current ? (
          current.type === 'video' ? (
            <VideoPlayer key={current.url} src={current.url} />
          ) : current.type === 'camera' ? (
            <img
              key={current.url}
              src={current.url}
              alt="摄像头实时画面"
              className="h-full w-full object-contain"
            />
          ) : (
            <img
              key={current.url}
              src={current.url}
              alt={current.name}
              className="h-full w-full object-contain"
            />
          )
        ) : (
          <div className="h-full flex flex-col items-center justify-center text-center text-white/50 px-6">
            <div className="text-7xl mb-5 opacity-20">🎬</div>
            <p className="text-sm leading-relaxed">上传施工现场图片或视频<br />或连接监控摄像头</p>
            <button
              onClick={() => fileRef.current?.click()}
              className="mt-5 px-5 py-2 bg-indigo-600 hover:bg-indigo-500 text-white text-sm rounded-xl transition-colors"
            >
              选择文件
            </button>
          </div>
        )}
      </div>

      {/* Bottom floating controls */}
      <div className="absolute bottom-0 left-0 right-0 bg-gradient-to-t from-black/90 via-black/60 to-transparent pt-10 pb-3 px-3 space-y-2">
        {/* Thumbnails */}
        {items.length > 0 && (
          <div className="flex gap-2 overflow-x-auto pb-1 scrollbar-none">
            {items.map((item, i) => (
              <button
                key={i}
                onClick={() => selectItem(i, item.url, item.type)}
                className={`shrink-0 w-11 h-11 rounded-lg border-2 overflow-hidden transition-all ${
                  i === active ? 'border-indigo-400' : 'border-white/20 opacity-60 hover:opacity-100 hover:border-white/50'
                }`}
              >
                {item.type === 'image' ? (
                  <img src={item.url} alt="" className="w-full h-full object-cover" />
                ) : item.type === 'camera' ? (
                  <div className="w-full h-full bg-gray-800 flex items-center justify-center text-lg">📷</div>
                ) : (
                  <div className="w-full h-full bg-gray-700 flex items-center justify-center text-white text-sm">▶</div>
                )}
              </button>
            ))}
          </div>
        )}

        {/* Expandable: URL input */}
        {inputMode === 'url' && (
          <div className="flex gap-2">
            <input
              type="text"
              value={urlInput}
              onChange={(e) => setUrlInput(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && addFromUrl()}
              placeholder="输入图片/视频 URL…"
              autoFocus
              className="flex-1 bg-white/10 text-white text-xs rounded-lg px-3 py-2 placeholder-white/40 border border-white/20 focus:outline-none focus:border-indigo-400"
            />
            <button onClick={addFromUrl} className="px-3 py-2 bg-indigo-600 hover:bg-indigo-500 text-white text-xs rounded-lg transition-colors">
              添加
            </button>
            <button onClick={() => setInputMode('none')} className="px-2 py-2 bg-white/10 hover:bg-white/20 text-white/70 text-xs rounded-lg transition-colors">
              ✕
            </button>
          </div>
        )}

        {/* Expandable: RTSP camera input */}
        {inputMode === 'camera' && (
          <div className="space-y-1.5">
            <input
              type="text"
              value={rtspInput}
              onChange={(e) => setRtspInput(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && connectCamera()}
              placeholder="rtsp://user:pass@192.168.1.10:554/stream"
              autoFocus
              className="w-full bg-white/10 text-white text-xs rounded-lg px-3 py-2 placeholder-white/40 border border-white/20 focus:outline-none focus:border-indigo-400"
            />
            <div className="flex gap-2">
              <button
                onClick={connectCamera}
                disabled={!rtspInput.trim()}
                className="flex-1 py-1.5 bg-indigo-600 hover:bg-indigo-500 disabled:opacity-40 text-white text-xs rounded-lg transition-colors"
              >
                连接摄像头
              </button>
              <button onClick={() => setInputMode('none')} className="px-3 py-1.5 bg-white/10 hover:bg-white/20 text-white/70 text-xs rounded-lg transition-colors">
                取消
              </button>
            </div>
          </div>
        )}

        {/* Action buttons */}
        <div className="flex gap-1.5">
          <button
            onClick={() => fileRef.current?.click()}
            className="flex-1 py-2 bg-white/10 hover:bg-white/20 text-white/90 text-xs rounded-xl border border-white/10 backdrop-blur-sm transition-colors"
          >
            📁 上传
          </button>
          <button
            onClick={() => toggleMode('url')}
            className={`flex-1 py-2 text-white/90 text-xs rounded-xl border backdrop-blur-sm transition-colors ${
              inputMode === 'url' ? 'bg-indigo-600/60 border-indigo-400/40' : 'bg-white/10 hover:bg-white/20 border-white/10'
            }`}
          >
            🔗 URL
          </button>
          <button
            onClick={() => toggleMode('camera')}
            className={`flex-1 py-2 text-white/90 text-xs rounded-xl border backdrop-blur-sm transition-colors ${
              inputMode === 'camera' ? 'bg-indigo-600/60 border-indigo-400/40' : 'bg-white/10 hover:bg-white/20 border-white/10'
            }`}
          >
            � 摄像头
          </button>
        </div>
      </div>

      <input
        ref={fileRef}
        type="file"
        accept="image/*,video/*"
        multiple
        className="hidden"
        onChange={(e) => handleFiles(e.target.files)}
      />
    </div>
  )
}
