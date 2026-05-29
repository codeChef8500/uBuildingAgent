import { useEffect, useRef } from 'react'

interface Props {
  text: string
  running: boolean
}

export function StreamingText({ text, running }: Props) {
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [text])

  if (!text && !running) return null

  return (
    <div className="bg-gray-50 rounded-lg border border-gray-200 p-3 max-h-48 overflow-y-auto">
      <p className="text-xs text-gray-400 mb-2 font-semibold">编排 Agent 输出</p>
      <pre className="text-xs text-gray-700 whitespace-pre-wrap font-mono leading-relaxed">
        {text}
        {running && (
          <span className="inline-block w-1.5 h-3 bg-blue-500 ml-0.5 animate-pulse align-middle" />
        )}
      </pre>
      <div ref={bottomRef} />
    </div>
  )
}
