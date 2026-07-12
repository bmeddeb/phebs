// mark-scene.jsx — Context Port logo animation (phebs)
// Reads Stage/Sprite/useTime/Easing/interpolate from window (set by animations.jsx).
// NOTE: no top-level const of those names — they already exist in the shared eval scope.
/* global window */

// Timeline (seconds):
// 0.00–0.90  port draws on (stroke reveal, clockwise from gap edge)
// 0.75–1.05  dashed approach line fades in
// 1.10–2.00  context dot flies in from outside, docks through the gap (easeOutBack)
// 1.95–2.35  dock pop (dot scale 1 → 1.25 → 1)
// 2.15–2.90  accept pulse ring expands + fades
// 2.60–3.15  wordmark fades up under the mark
// 4.20–4.55  everything fades; loop restarts clean
function MarkAnim({ dark }) {
  const { useTime, Easing, interpolate } = window;
  const t = useTime();
  const port = dark ? '#FFFFFF' : '#000000';
  const dot = dark ? '#5E8BDB' : '#276EF1';
  const ghost = dark ? '#5D5D5D' : '#A6A6A6';

  const drawOffset = interpolate([0, 0.9], [100, 0], Easing.easeInOutCubic)(t);
  const approachO = interpolate([0.75, 1.05, 2.0, 2.3], [0, 1, 1, 0])(t);
  const dotX = interpolate([1.1, 2.0], [26.5, 15.5], Easing.easeOutBack)(t);
  const dotO = interpolate([1.1, 1.3], [0, 1])(t);
  const pop = interpolate([1.95, 2.15, 2.35], [1, 1.25, 1])(t);
  const pulseR = interpolate([2.15, 2.9], [2.2, 7.5], Easing.easeOutCubic)(t);
  const pulseO = interpolate([2.15, 2.3, 2.9], [0, 0.4, 0])(t);
  const wordO = interpolate([2.6, 3.15], [0, 1])(t);
  const wordY = interpolate([2.6, 3.15], [33, 31.5], Easing.easeOutCubic)(t);
  const rootO = interpolate([4.2, 4.55], [1, 0])(t);

  return (
    <div style={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center', opacity: rootO }}>
      <svg width="680" viewBox="0 0 48 36" fill="none" style={{ display: 'block' }}>
        <g transform="translate(10.75 1)">
          <path
            d="M21 8.5 V 7.5 A 4.5 4.5 0 0 0 16.5 3 H 7.5 A 4.5 4.5 0 0 0 3 7.5 V 16.5 A 4.5 4.5 0 0 0 7.5 21 H 16.5 A 4.5 4.5 0 0 0 21 16.5 V 15.5"
            pathLength="100"
            strokeDasharray="100"
            strokeDashoffset={drawOffset}
            stroke={port}
            strokeWidth="1.8"
            strokeLinecap="round"
          />
          <path d="M26 12 H 18.5" stroke={ghost} strokeWidth="0.9" strokeDasharray="1.8 1.6" opacity={approachO} />
          <circle cx="15.5" cy="12" r={pulseR} stroke={dot} strokeWidth="0.5" opacity={pulseO} />
          <circle cx={dotX} cy="12" r={1.9 * pop} fill={dot} opacity={dotO} />
        </g>
        <text
          x="24"
          y={wordY}
          textAnchor="middle"
          fontFamily="system-ui, 'Helvetica Neue', Helvetica, Arial, sans-serif"
          fontSize="7"
          fontWeight="700"
          fill={port}
          opacity={wordO}
        >
          phebs
        </text>
      </svg>
    </div>
  );
}

function MarkScene({ dark }) {
  const { Stage, Sprite } = window;
  const isDark = dark === true || dark === 'true';
  return (
    <Stage width={960} height={560} duration={4.6} loop autoplay background={isDark ? '#000000' : '#FFFFFF'}>
      <Sprite start={0} end={4.6}>
        <MarkAnim dark={isDark} />
      </Sprite>
    </Stage>
  );
}

window.MarkScene = MarkScene;
