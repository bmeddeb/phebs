import { ParagraphSmall } from 'baseui/typography'

// T5.3 fills this in (CodeMirror viewer).
export default function FilePage({ params }: { params: URLSearchParams }) {
  return (
    <ParagraphSmall>
      File viewer for {params.get('repo')}/{params.get('path')} lands with T5.3.
    </ParagraphSmall>
  )
}
