import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { useCopy } from './useCopy'

function Probe({ value = 'first' }: { value?: string }) {
  const [state, copy] = useCopy(value)
  return <button onClick={() => void copy(value)}>{state}</button>
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.useRealTimers() })

test.each(['missing', 'denied', 'throws'])('clipboard %s reports failure', async failure => {
  const clipboard = failure === 'missing' ? undefined : { writeText: vi.fn(() => {
    if (failure === 'throws') throw new Error('denied')
    return Promise.reject(new Error('denied'))
  }) }
  vi.stubGlobal('navigator', { clipboard })
  render(<Probe />)
  fireEvent.click(screen.getByRole('button'))
  expect(await screen.findByText('failed')).toBeTruthy()
})

test('a changed value cannot inherit an older successful copy', async () => {
  let finish!: () => void
  const writeText = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
  vi.stubGlobal('navigator', { clipboard: { writeText } })
  const view = render(<Probe />)
  fireEvent.click(screen.getByRole('button'))
  expect(screen.getByText('idle')).toBeTruthy()
  view.rerender(<Probe value="replacement" />)
  await act(async () => finish())
  expect(screen.getByText('idle')).toBeTruthy()
  expect(writeText).toHaveBeenCalledWith('first')
})

test('a later copy attempt owns the feedback and its single cleanup timer', async () => {
  vi.useFakeTimers()
  const finishes: (() => void)[] = []
  vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn(() => new Promise<void>(resolve => { finishes.push(resolve) })) } })
  const view = render(<Probe />)
  fireEvent.click(screen.getByRole('button'))
  fireEvent.click(screen.getByRole('button'))
  await act(async () => finishes[1]())
  expect(screen.getByText('done')).toBeTruthy()
  expect(vi.getTimerCount()).toBe(1)
  await act(async () => finishes[0]())
  expect(vi.getTimerCount()).toBe(1)
  act(() => vi.advanceTimersByTime(1600))
  expect(screen.getByText('idle')).toBeTruthy()
  fireEvent.click(screen.getByRole('button'))
  view.unmount()
  await act(async () => finishes[2]())
  expect(vi.getTimerCount()).toBe(0)
})
