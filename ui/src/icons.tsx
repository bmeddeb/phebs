// Inline 16×16 icons, stroke: currentColor, 1.5px — the design handoff's icon
// set. Sized via the `size` prop; color inherits from the parent.
type IconProps = { size?: number }

function svg(size: number, children: React.ReactNode) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      {children}
    </svg>
  )
}

export const SearchIcon = ({ size = 16 }: IconProps) =>
  svg(size, <><circle cx="7" cy="7" r="4.5" /><path d="M10.5 10.5 14 14" /></>)

export const CopyIcon = ({ size = 16 }: IconProps) =>
  svg(size, <><rect x="5.5" y="5.5" width="7.5" height="7.5" rx="1.5" /><path d="M10.5 5.5V4A1.5 1.5 0 0 0 9 2.5H4A1.5 1.5 0 0 0 2.5 4v5A1.5 1.5 0 0 0 4 10.5h1.5" /></>)

export const CheckIcon = ({ size = 16 }: IconProps) =>
  svg(size, <path d="m3 8.5 3.2 3.2L13 4.5" />)

export const OpenIcon = ({ size = 16 }: IconProps) =>
  svg(size, <><path d="M6.5 3H3.5A1.5 1.5 0 0 0 2 4.5v8A1.5 1.5 0 0 0 3.5 14h8A1.5 1.5 0 0 0 13 12.5v-3" /><path d="M9.5 2.5H13.5V6.5" /><path d="M13.5 2.5 7.5 8.5" /></>)

export const ChevronRight = ({ size = 16 }: IconProps) => svg(size, <path d="m6 4 4 4-4 4" />)
export const ChevronDown = ({ size = 16 }: IconProps) => svg(size, <path d="m4 6 4 4 4-4" />)
export const CloseIcon = ({ size = 16 }: IconProps) => svg(size, <path d="M4 4l8 8M12 4l-8 8" />)
export const StopIcon = ({ size = 16 }: IconProps) => svg(size, <rect x="4" y="4" width="8" height="8" rx="1.5" fill="currentColor" stroke="none" />)

export const CommitIcon = ({ size = 16 }: IconProps) =>
  svg(size, <><circle cx="8" cy="8" r="2.5" /><path d="M8 2v3.5M8 10.5V14" /></>)

export const SunIcon = ({ size = 16 }: IconProps) =>
  svg(size, <><circle cx="8" cy="8" r="3" /><path d="M8 1v1.5M8 13.5V15M1 8h1.5M13.5 8H15M3 3l1 1M12 12l1 1M13 3l-1 1M4 12l-1 1" /></>)

export const MoonIcon = ({ size = 16 }: IconProps) =>
  svg(size, <path d="M13.5 9.5A5.5 5.5 0 0 1 6.5 2.5 5.5 5.5 0 1 0 13.5 9.5Z" />)

export const WarningIcon = ({ size = 16 }: IconProps) =>
  svg(size, <><path d="M8 2 15 14H1L8 2Z" /><path d="M8 6.5v3.5M8 12h.01" /></>)

export const KeyIcon = ({ size = 16 }: IconProps) =>
  svg(size, <><circle cx="5.25" cy="10.75" r="2.75" /><path d="m7.25 8.75 5.5-5.5M10 6h2v2" /></>)

export const TrashIcon = ({ size = 16 }: IconProps) =>
  svg(size, <><path d="M3.5 4.5h9M6 4.5V3h4v1.5M5 6.5v6M8 6.5v6M11 6.5v6M4.5 4.5l.5 9h6l.5-9" /></>)

export const LogoutIcon = ({ size = 16 }: IconProps) =>
  svg(size, <><path d="M7 3H3.5A1.5 1.5 0 0 0 2 4.5v7A1.5 1.5 0 0 0 3.5 13H7" /><path d="M9.5 5 13 8l-3.5 3M13 8H6" /></>)

export const AuditIcon = ({ size = 16 }: IconProps) =>
  svg(size, <><rect x="3" y="2" width="10" height="12" rx="1.5" /><path d="M5.5 5.5h5M5.5 8h5M5.5 10.5h3" /></>)
