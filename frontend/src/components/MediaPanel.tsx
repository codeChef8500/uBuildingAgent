import { useRef, useState } from 'react'
import { VideoPlayer } from './VideoPlayer'
import type { FrameEntry } from '../hooks/useVideoInspect'

interface MediaItem {
  url: string
  type: 'image' | 'video' | 'camera'
  name: string
}

interface Props {
  onMediaChange: (url: string, type: 'image' | 'video' | 'camera' | 'none', name?: string) => void
  frames?: FrameEntry[]
  videoSeekTrigger?: { time: number; triggerId: number } | null
}

export function MediaPanel({ onMediaChange, frames, videoSeekTrigger }: Props) {
  const [items, setItems] = useState<MediaItem[]>([])
  const [active, setActive] = useState(0)
  const fileRef = useRef<HTMLInputElement>(null)

  function selectItem(idx: number, url: string, type: MediaItem['type'], name?: string) {
    setActive(idx)
    onMediaChange(url, type, name)
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
    selectItem(next.length - newItems.length, first.url, first.type, first.name)
  }

  const current = items[active] ?? null

  return (
    <div className="relative h-full bg-black overflow-hidden flex flex-col">
      {/* Main viewer */}
      <div className="flex-1 min-h-0 relative">
        {current ? (
          current.type === 'video' ? (
            <VideoPlayer
              key={current.url}
              src={current.url}
              frames={frames}
              videoSeekTrigger={videoSeekTrigger}
            />
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
            <div className="text-7xl mb-5 opacity-20 animate-pulse">🎬</div>
            <p className="text-base font-medium text-white mb-2">AI 视频逐帧安全巡检</p>
            <p className="text-xs text-gray-400 leading-relaxed max-w-sm">
              上传施工现场视频或输入视频 URL 启动 Agent 进行逐帧分析
            </p>
            <button
              onClick={() => fileRef.current?.click()}
              className="mt-5 px-5 py-2.5 bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium rounded-xl transition-colors shadow-lg"
            >
              选择视频文件
            </button>
          </div>
        )}
      </div>

      {/* Bottom floating controls */}
      {items.length > 0 && (
        <div className="absolute bottom-0 left-0 right-0 bg-gradient-to-t from-black/90 via-black/60 to-transparent pt-10 pb-3 px-3 space-y-2">
          {/* Thumbnails */}
          <div className="flex gap-2 overflow-x-auto pb-1 scrollbar-none">
            {items.map((item, i) => (
              <button
                key={i}
                onClick={() => selectItem(i, item.url, item.type, item.name)}
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
        </div>
      )}

      <input
        ref={fileRef}
        type="file"
        accept="video/*"
        multiple
        className="hidden"
        onChange={(e) => handleFiles(e.target.files)}
      />
    </div>
  )
}
