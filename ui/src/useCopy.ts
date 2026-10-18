import { useCallback, useEffect, useRef, useState } from 'react'

type CopyState = 'idle' | 'done' | 'failed'

export function useCopy(identity: unknown): [CopyState, (value: string) => Promise<void>] {
  const [state, setState] = useState<CopyState>('idle')
  const pending = useRef({ generation: 0, timer: 0 })
  useEffect(() => {
    const active = pending.current
    setState('idle')
    return () => {
      active.generation++
      window.clearTimeout(active.timer)
    }
  }, [identity])
  const copy = useCallback(async (value: string) => {
    const active = pending.current
    const current = ++active.generation
    window.clearTimeout(active.timer)
    setState('idle')
    let next: CopyState = 'done'
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable')
      await navigator.clipboard.writeText(value)
    } catch {
      next = 'failed'
    }
    if (current !== active.generation) return
    setState(next)
    if (next === 'done') active.timer = window.setTimeout(() => setState('idle'), 1600)
  }, [])
  return [state, copy]
}
