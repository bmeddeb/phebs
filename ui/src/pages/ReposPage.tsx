import { useCallback, useEffect, useState } from 'react'
import {
  StyledTable,
  StyledTableHead,
  StyledTableHeadRow,
  StyledTableHeadCell,
  StyledTableBody,
  StyledTableBodyRow,
  StyledTableBodyCell,
} from 'baseui/table-semantic'
import { Button, SIZE, KIND as BUTTON_KIND } from 'baseui/button'
import { Tag, KIND as TAG_KIND } from 'baseui/tag'
import { Notification, KIND } from 'baseui/notification'
import { ParagraphSmall } from 'baseui/typography'
import { Spinner } from 'baseui/spinner'
import { fetchRepoStatus, postReindex } from '../api'
import type { RepoStatus } from '../api'

// T5.4: repo table over /api/repo-status, polled so job-state transitions
// (and the reindex button's effect) show up within one cycle.
const POLL_MS = 3000

export default function ReposPage() {
  const [repos, setRepos] = useState<RepoStatus[] | null>(null)
  const [error, setError] = useState('')

  const refresh = useCallback(() => {
    fetchRepoStatus()
      .then((r) => {
        setRepos(r)
        setError('')
      })
      .catch((e) => setError(String(e)))
  }, [])

  useEffect(() => {
    refresh()
    const id = setInterval(refresh, POLL_MS)
    return () => clearInterval(id)
  }, [refresh])

  const reindex = (name: string) => {
    postReindex(name, true).then(refresh).catch((e) => setError(String(e)))
  }

  if (error)
    return (
      <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto' } } }}>
        {error}
      </Notification>
    )
  if (repos === null) return <Spinner $size="small" />
  if (repos.length === 0)
    return <ParagraphSmall>No repos yet — add a connection to the config and restart.</ParagraphSmall>

  return (
    <StyledTable>
      <StyledTableHead>
        <StyledTableHeadRow>
          <StyledTableHeadCell>Repo</StyledTableHeadCell>
          <StyledTableHeadCell>Connections</StyledTableHeadCell>
          <StyledTableHeadCell>Index status</StyledTableHeadCell>
          <StyledTableHeadCell>Indexed at</StyledTableHeadCell>
          <StyledTableHeadCell>Commit</StyledTableHeadCell>
          <StyledTableHeadCell></StyledTableHeadCell>
        </StyledTableHeadRow>
      </StyledTableHead>
      <StyledTableBody>
        {repos.map((r) => (
          <StyledTableBodyRow key={r.name}>
            <StyledTableBodyCell>
              {r.name}
              {r.orphaned && (
                <Tag closeable={false} kind={TAG_KIND.orange}>
                  orphaned
                </Tag>
              )}
            </StyledTableBodyCell>
            <StyledTableBodyCell>{r.connections?.join(', ') ?? '—'}</StyledTableBodyCell>
            <StyledTableBodyCell>
              <JobState repo={r} />
            </StyledTableBodyCell>
            <StyledTableBodyCell>
              {r.indexed_at ? new Date(r.indexed_at).toLocaleString() : '—'}
            </StyledTableBodyCell>
            <StyledTableBodyCell>
              <code>{r.indexed_commit_hash?.slice(0, 10) ?? '—'}</code>
            </StyledTableBodyCell>
            <StyledTableBodyCell>
              <Button size={SIZE.compact} kind={BUTTON_KIND.secondary} onClick={() => reindex(r.name)}>
                Reindex
              </Button>
            </StyledTableBodyCell>
          </StyledTableBodyRow>
        ))}
      </StyledTableBody>
    </StyledTable>
  )
}

function JobState({ repo }: { repo: RepoStatus }) {
  const job = repo.last_index_job
  if (!job) return <Tag closeable={false}>never indexed</Tag>
  const kind =
    job.status === 'done'
      ? TAG_KIND.green
      : job.status === 'failed'
        ? TAG_KIND.red
        : TAG_KIND.blue
  return (
    <>
      <Tag closeable={false} kind={kind}>
        {job.status}
      </Tag>
      {job.error && <ParagraphSmall margin={0}>{job.error}</ParagraphSmall>}
    </>
  )
}
