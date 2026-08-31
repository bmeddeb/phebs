import { useCallback, useId, type ReactNode } from 'react'
import { useStyletron } from 'baseui'
import {
  Button,
  KIND as BUTTON_KIND,
  SIZE as BUTTON_SIZE,
} from 'baseui/button'
import { Modal, ROLE as MODAL_ROLE } from 'baseui/modal'
import {
  TYPE,
  focusRing,
  panelTransition,
  usePhebsTokens,
  type PhebsTokens,
} from '../theme'

export interface ConfirmDialogProps {
  isOpen: boolean
  title: string
  detail: ReactNode
  confirmLabel: string
  cancelLabel?: string
  onConfirm: () => void
  onCancel: () => void
}

/**
 * The house confirmation surface. Cancellation is deliberately the first and
 * initially focused action; only the explicit confirm button can accept.
 */
export function ConfirmDialog({
  isOpen,
  ...props
}: ConfirmDialogProps) {
  if (!isOpen) return null
  return <OpenConfirmDialog {...props} />
}

function OpenConfirmDialog({
  title,
  detail,
  confirmLabel,
  cancelLabel = 'Cancel',
  onConfirm,
  onCancel,
}: Omit<ConfirmDialogProps, 'isOpen'>) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const titleId = useId()
  const detailId = useId()
  const focusCancel = useCallback((node: HTMLButtonElement | null) => {
    if (node) node.focus({ preventScroll: true })
  }, [])

  return (
    <Modal
      isOpen
      role={MODAL_ROLE.alertdialog}
      size="440px"
      autoFocus
      focusLock
      returnFocus={{ preventScroll: true }}
      closeable
      onClose={onCancel}
      overrides={{
        DialogContainer: {
          style: {
            ...panelTransition(),
            padding: '12px',
            boxSizing: 'border-box',
          },
        },
        Dialog: {
          props: {
            'aria-label': undefined,
            'aria-labelledby': titleId,
            'aria-describedby': detailId,
          },
          style: {
            ...panelTransition(),
            display: 'grid',
            gridTemplateRows: 'auto minmax(0, 1fr) auto',
            boxSizing: 'border-box',
            width: '440px',
            maxWidth: 'calc(100vw - 24px)',
            maxHeight: 'calc(100vh - 24px)',
            margin: 0,
            overflow: 'hidden',
            border: `1px solid ${tok.cardBorder}`,
            borderRadius: '8px',
            backgroundColor: tok.pageBg,
            color: tok.textPrimary,
            boxShadow: tok.popoverShadow,
          },
        },
        Close: {
          style: {
            color: tok.textSecondary,
            ':hover': { color: tok.textPrimary },
            ':focus-visible': focusRing(tok),
          },
        },
      }}
    >
      <h2
        id={titleId}
        className={css({
          ...TYPE.heading,
          margin: 0,
          minWidth: 0,
          padding: '18px 48px 8px 20px',
          overflowWrap: 'anywhere',
          color: tok.textPrimary,
        })}
      >
        {title}
      </h2>
      <div
        id={detailId}
        className={css({
          ...TYPE.small,
          minWidth: 0,
          minHeight: 0,
          maxHeight: '360px',
          padding: '0 20px 18px',
          overflowY: 'auto',
          overscrollBehavior: 'contain',
          overflowWrap: 'anywhere',
          color: tok.textSecondary,
        })}
      >
        {detail}
      </div>
      <div
        className={css({
          display: 'grid',
          gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
          gap: '8px',
          padding: '12px 20px 16px',
          borderTop: `1px solid ${tok.innerSep}`,
          '@media screen and (max-width: 480px)': {
            gridTemplateColumns: 'minmax(0, 1fr)',
          },
        })}
      >
        <Button
          ref={focusCancel}
          type="button"
          size={BUTTON_SIZE.compact}
          kind={BUTTON_KIND.tertiary}
          onClick={onCancel}
          overrides={actionOverrides(tok)}
        >
          {cancelLabel}
        </Button>
        <Button
          type="button"
          size={BUTTON_SIZE.compact}
          kind={BUTTON_KIND.secondary}
          onClick={onConfirm}
          overrides={actionOverrides(tok)}
        >
          {confirmLabel}
        </Button>
      </div>
    </Modal>
  )
}

function actionOverrides(tok: PhebsTokens) {
  return {
    BaseButton: {
      style: {
        width: '100%',
        minWidth: 0,
        minHeight: '36px',
        whiteSpace: 'normal' as const,
        overflowWrap: 'anywhere' as const,
        fontSize: '11px',
        lineHeight: '16px',
        ':focus-visible': focusRing(tok),
      },
    },
  }
}
