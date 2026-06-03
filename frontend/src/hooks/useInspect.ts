import { useState, useCallback, useRef } from 'react'
import type {
  AgentEvent,
  SubAgentState,
  SubAgentKey,
  ToolCallEntry,
  InspectionContext,
} from '../types/agent'
import { PIPELINE_STAGES } from '../types/agent'

function initSubAgents(): SubAgentState[] {
  return PIPELINE_STAGES.map((s) => ({
    key: s.key,
    status: 'pending',
    tools: [],
  }))
}

function parsePartialJSON(jsonStr: string): any {
  let cleanStr = jsonStr.trim();
  if (!cleanStr) return null;

  let inString = false;
  let escape = false;
  const stack: ('{' | '[')[] = [];

  for (let i = 0; i < cleanStr.length; i++) {
    const char = cleanStr[i];
    if (escape) {
      escape = false;
      continue;
    }
    if (char === '\\') {
      if (inString) {
        escape = true;
      }
      continue;
    }
    if (char === '"') {
      inString = !inString;
      continue;
    }
    if (!inString) {
      if (char === '{') {
        stack.push('{');
      } else if (char === '[') {
        stack.push('[');
      } else if (char === '}') {
        if (stack[stack.length - 1] === '{') {
          stack.pop();
        }
      } else if (char === ']') {
        if (stack[stack.length - 1] === '[') {
          stack.pop();
        }
      }
    }
  }

  let suffix = '';
  if (inString) {
    suffix += '"';
  }
  for (let i = stack.length - 1; i >= 0; i--) {
    if (stack[i] === '{') {
      suffix += '}';
    } else if (stack[i] === '[') {
      suffix += ']';
    }
  }

  try {
    return JSON.parse(cleanStr + suffix);
  } catch (e) {
    return null;
  }
}

function tryParseContext(text: string): InspectionContext | null {
  const marker = '[REPORT_JSON]';
  const markerIdx = text.indexOf(marker);
  if (markerIdx === -1) {
    // Backward-compatible fallback
    const fallbackIdx = text.indexOf('{"input"');
    if (fallbackIdx === -1) return null;
    const jsonStr = text.slice(fallbackIdx);
    return parsePartialJSON(jsonStr) as InspectionContext | null;
  }
  const jsonStr = text.slice(markerIdx + marker.length);
  return parsePartialJSON(jsonStr) as InspectionContext | null;
}

export type InspectStatus = 'idle' | 'running' | 'done' | 'error'

export interface InspectState {
  status: InspectStatus
  subAgents: SubAgentState[]
  streamingText: string
  report: InspectionContext | null
  errorMsg: string
}

export function useInspect() {
  const [state, setState] = useState<InspectState>({
    status: 'idle',
    subAgents: initSubAgents(),
    streamingText: '',
    report: null,
    errorMsg: '',
  })

  const abortRef = useRef<AbortController | null>(null)

  const start = useCallback(
    async (sceneDescription: string, imageUrl: string, location: string) => {
      if (abortRef.current) abortRef.current.abort()
      abortRef.current = new AbortController()

      setState({
        status: 'running',
        subAgents: initSubAgents(),
        streamingText: '',
        report: null,
        errorMsg: '',
      })

      try {
        const res = await fetch('/api/safeagent/inspect', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            scene_description: sceneDescription,
            image_url: imageUrl || undefined,
            location: location || undefined,
          }),
          signal: abortRef.current.signal,
        })

        if (!res.ok || !res.body) {
          throw new Error(`HTTP ${res.status}`)
        }

        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''
        let fullText = ''

        // Track which sub-agent is currently active
        let activeSubAgentKey: SubAgentKey | null = null

        while (true) {
          const { done, value } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() ?? ''

          for (const line of lines) {
            if (!line.startsWith('data: ')) continue
            const raw = line.slice(6).trim()
            if (!raw) continue

            let ev: AgentEvent
            try {
              ev = JSON.parse(raw) as AgentEvent
            } catch {
              continue
            }

            if (ev.type === 'text_delta' && ev.delta) {
              fullText += ev.delta
              const report = tryParseContext(fullText)

              // Filter out the JSON report block from streamingText so it stays clean
              let displayStream = fullText
              const marker = '[REPORT_JSON]'
              const jsonStart = fullText.indexOf(marker)
              if (jsonStart !== -1) {
                displayStream = fullText.slice(0, jsonStart).trim()
              } else {
                // Backward-compatible fallback
                const fallbackStart = fullText.indexOf('{"input"')
                if (fallbackStart !== -1) {
                  displayStream = fullText.slice(0, fallbackStart).trim()
                }
              }

              setState((prev) => ({
                ...prev,
                streamingText: displayStream,
                report: report ?? prev.report,
              }))
            } else if (ev.type === 'tool_start' && ev.tool_call) {
              const tc = ev.tool_call
              if (tc.name === 'Task') {
                // Identify which sub-agent is being called
                let subType: SubAgentKey | null = null
                try {
                  const args = JSON.parse(tc.args) as Record<string, unknown>
                  subType = (args.subagent_type as SubAgentKey) ?? null
                } catch {
                  /* ignore */
                }

                if (subType) {
                  activeSubAgentKey = subType
                  setState((prev) => ({
                    ...prev,
                    subAgents: prev.subAgents.map((sa) =>
                      sa.key === subType
                        ? { ...sa, status: 'running', taskCallId: tc.id }
                        : sa,
                    ),
                  }))
                }
              } else if (activeSubAgentKey) {
                // inner tool of the active sub-agent
                const entry: ToolCallEntry = {
                  id: tc.id,
                  name: tc.name,
                  args: tc.args,
                  running: true,
                }
                const key = activeSubAgentKey
                setState((prev) => ({
                  ...prev,
                  subAgents: prev.subAgents.map((sa) =>
                    sa.key === key
                      ? { ...sa, tools: [...sa.tools, entry] }
                      : sa,
                  ),
                }))
              }
            } else if (ev.type === 'tool_end' && ev.tool_call) {
              const tc = ev.tool_call

              if (tc.name === 'Task') {
                // Mark the sub-agent whose taskCallId matches
                setState((prev) => ({
                  ...prev,
                  subAgents: prev.subAgents.map((sa) =>
                    sa.taskCallId === tc.id
                      ? { ...sa, status: 'done', output: ev.tool_result }
                      : sa,
                  ),
                }))
                activeSubAgentKey = null
              } else if (activeSubAgentKey) {
                const key = activeSubAgentKey
                setState((prev) => ({
                  ...prev,
                  subAgents: prev.subAgents.map((sa) =>
                    sa.key === key
                      ? {
                          ...sa,
                          tools: sa.tools.map((t) =>
                            t.id === tc.id
                              ? { ...t, result: ev.tool_result, running: false }
                              : t,
                          ),
                        }
                      : sa,
                  ),
                }))
              }
            } else if (ev.type === 'error') {
              setState((prev) => ({
                ...prev,
                status: 'error',
                errorMsg: ev.err ?? 'Unknown error',
              }))
              return
            }
          }
        }

        // Final pass: try to parse report from full text
        const finalReport = tryParseContext(fullText)
        setState((prev) => ({
          ...prev,
          status: 'done',
          report: finalReport ?? prev.report,
          subAgents: prev.subAgents.map((sa) =>
            sa.status === 'running' ? { ...sa, status: 'done' } : sa,
          ),
        }))
      } catch (err) {
        if ((err as Error).name === 'AbortError') return
        setState((prev) => ({
          ...prev,
          status: 'error',
          errorMsg: String(err),
        }))
      }
    },
    [],
  )

  const reset = useCallback(() => {
    abortRef.current?.abort()
    setState({
      status: 'idle',
      subAgents: initSubAgents(),
      streamingText: '',
      report: null,
      errorMsg: '',
    })
  }, [])

  return { state, start, reset }
}
