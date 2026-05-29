import { useState, useEffect } from 'react'

interface Props {
  imageUrl: string
  onStart: (description: string, imageUrl: string, location: string) => void
  onReset: () => void
  running: boolean
}

const SendIcon = () => (
  <svg width="18" height="18" viewBox="0 0 18 18" fill="none">
    <path d="M2 9l13-6-3 6 3 6-13-6z" fill="currentColor"/>
  </svg>
)

const StopIcon = () => (
  <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
    <rect x="2" y="2" width="10" height="10" rx="2.5" fill="currentColor"/>
  </svg>
)

export function SceneInputForm({ imageUrl, onStart, onReset, running }: Props) {
  const [description, setDescription] = useState('')
  const [urlField, setUrlField] = useState(imageUrl)

  useEffect(() => { setUrlField(imageUrl) }, [imageUrl])

  function handleSubmit() {
    if (!description.trim() || running) return
    onStart(description.trim(), urlField.trim(), '')
  }

  return (
    <div className="flex items-end gap-2">
      {/* Inputs column */}
      <div className="flex-1 space-y-1.5">
        <textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey && !running) {
              e.preventDefault()
              handleSubmit()
            }
          }}
          placeholder="描述施工现场情况，按 Enter 发送…"
          rows={2}
          disabled={running}
          className="w-full bg-gray-100 text-gray-800 text-sm rounded-xl px-3 py-2 placeholder-gray-400 border border-gray-200 focus:outline-none focus:border-indigo-400 resize-none disabled:opacity-50"
        />
      </div>

      {/* Send / Stop button */}
      {running ? (
        <button
          onClick={onReset}
          className="shrink-0 w-11 h-11 rounded-full bg-red-100 hover:bg-red-200 text-red-500 flex items-center justify-center transition-colors mb-0.5"
          title="停止巡检"
        >
          <StopIcon />
        </button>
      ) : (
        <button
          onClick={handleSubmit}
          disabled={!description.trim()}
          className="shrink-0 w-11 h-11 rounded-full bg-indigo-600 hover:bg-indigo-500 disabled:opacity-30 disabled:cursor-not-allowed text-white flex items-center justify-center transition-colors mb-0.5"
          title="开始巡检 (Enter)"
        >
          <SendIcon />
        </button>
      )}
    </div>
  )
}
