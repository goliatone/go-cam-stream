import type { ComponentChildren } from "preact";

interface IconProps {
  size?: number;
}
function Svg({ size = 24, children }: IconProps & { children: ComponentChildren }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.8"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {children}
    </svg>
  );
}
const solid = { fill: "currentColor", stroke: "none" } as const;

export const PlayIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M8 5.6v12.8a.8.8 0 0 0 1.2.7l10-6.4a.8.8 0 0 0 0-1.4l-10-6.4A.8.8 0 0 0 8 5.6z" {...solid} />
  </Svg>
);
export const PauseIcon = (p: IconProps) => (
  <Svg {...p}>
    <rect x="6.5" y="5" width="4" height="14" rx="1.2" {...solid} />
    <rect x="13.5" y="5" width="4" height="14" rx="1.2" {...solid} />
  </Svg>
);
export const StopIcon = (p: IconProps) => (
  <Svg {...p}>
    <rect x="6.5" y="6.5" width="11" height="11" rx="2" {...solid} />
  </Svg>
);
export const VolumeIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 9.5h3.5L12 6v12l-4.5-3.5H4z" {...solid} />
    <path d="M15.5 9.2a4 4 0 0 1 0 5.6M18 6.7a7.5 7.5 0 0 1 0 10.6" />
  </Svg>
);
export const MutedIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 9.5h3.5L12 6v12l-4.5-3.5H4z" {...solid} />
    <path d="m16 9.5 5 5m0-5-5 5" />
  </Svg>
);
export const PipIcon = (p: IconProps) => (
  <Svg {...p}>
    <rect x="3" y="5" width="18" height="14" rx="2.5" />
    <rect x="12" y="11.5" width="7" height="5.5" rx="1.2" {...solid} />
  </Svg>
);
export const FullscreenIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 9V5.5A1.5 1.5 0 0 1 5.5 4H9M15 4h3.5A1.5 1.5 0 0 1 20 5.5V9M20 15v3.5a1.5 1.5 0 0 1-1.5 1.5H15M9 20H5.5A1.5 1.5 0 0 1 4 18.5V15" />
  </Svg>
);
export const ExitFullscreenIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M9 4v3.5A1.5 1.5 0 0 1 7.5 9H4M20 9h-3.5A1.5 1.5 0 0 1 15 7.5V4M15 20v-3.5a1.5 1.5 0 0 1 1.5-1.5H20M4 15h3.5A1.5 1.5 0 0 1 9 16.5V20" />
  </Svg>
);
export const SidebarIcon = (p: IconProps) => (
  <Svg {...p}>
    <rect x="3" y="4.5" width="18" height="15" rx="2.5" />
    <path d="M9 4.5v15" />
  </Svg>
);
export const KeyboardIcon = (p: IconProps) => (
  <Svg {...p}>
    <rect x="2.5" y="6" width="19" height="12" rx="2.5" />
    <path d="M6.5 10h.01M10 10h.01M13.5 10h.01M17 10h.01M7.5 14h9" />
  </Svg>
);
export const CameraIcon = (p: IconProps) => (
  <Svg {...p}>
    <rect x="3" y="6" width="13" height="12" rx="3" />
    <path d="m16 10 5-3v10l-5-3" />
  </Svg>
);
export const AlertIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M10.3 4.6 2.9 17.5A2 2 0 0 0 4.6 20.5h14.8a2 2 0 0 0 1.7-3L13.7 4.6a2 2 0 0 0-3.4 0z" />
    <path d="M12 9.5v4M12 17h.01" />
  </Svg>
);
export const OfflineIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M3 3l18 18" />
    <path d="M8.6 8.7A7 7 0 0 0 5 10.6M2 7.6a14 14 0 0 1 4-2.4M10.5 5.1a14 14 0 0 1 11.5 2.5M15.5 11.6a7 7 0 0 1 3.5 2M8.5 15.5a5 5 0 0 1 5.5-.9M12 19h.01" />
  </Svg>
);
export const LockIcon = (p: IconProps) => (
  <Svg {...p}>
    <rect x="5" y="10.5" width="14" height="10" rx="2.5" />
    <path d="M8 10.5V8a4 4 0 0 1 8 0v2.5" />
  </Svg>
);
export const RefreshIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3" />
    <path d="M19.5 4.5v4.5H15" />
  </Svg>
);
export const CloseIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M6 6l12 12M18 6 6 18" />
  </Svg>
);
export const GridIcon = (p: IconProps) => (
  <Svg {...p}>
    <rect x="3.5" y="4.5" width="7" height="6" rx="1.5" />
    <rect x="13.5" y="4.5" width="7" height="6" rx="1.5" />
    <rect x="3.5" y="13.5" width="7" height="6" rx="1.5" />
    <rect x="13.5" y="13.5" width="7" height="6" rx="1.5" />
  </Svg>
);
export const CopyIcon = (p: IconProps) => (
  <Svg {...p}>
    <rect x="8.5" y="8.5" width="11" height="11" rx="2" />
    <path d="M15.5 8.5V6A1.5 1.5 0 0 0 14 4.5H6A1.5 1.5 0 0 0 4.5 6v8A1.5 1.5 0 0 0 6 15.5h2.5" />
  </Svg>
);
export const CheckIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="m5 12.5 4.5 4.5L19 7.5" />
  </Svg>
);
export const MinusIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M6 12h12" />
  </Svg>
);
export const TuneIcon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 7h9M18 7h2M4 17h4M13 17h7" />
    <circle cx="15.5" cy="7" r="2.2" />
    <circle cx="10.5" cy="17" r="2.2" />
  </Svg>
);

export function BrandMark() {
  return (
    <svg class="brand-mark" width="28" height="28" viewBox="0 0 28 28" aria-hidden="true" focusable="false">
      <rect class="brand-bg" width="28" height="28" rx="8" />
      <rect class="brand-fg" x="5.5" y="9" width="12" height="10" rx="3" fill="none" stroke-width="1.8" />
      <path class="brand-fg" d="M17.5 12.6 22.5 10v8l-5-2.6" fill="none" stroke-width="1.8" stroke-linejoin="round" />
    </svg>
  );
}
