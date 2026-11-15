import type { ComponentChildren } from "preact";

// Inline stroke icons render the same in every browser, unlike Unicode glyphs.
function Icon({
  size = 24,
  children,
}: {
  size?: number;
  children: ComponentChildren;
}) {
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

export const CameraIcon = () => (
  <Icon>
    <rect x="3" y="6" width="13" height="12" rx="3" />
    <path d="m16 10 5-3v10l-5-3" />
  </Icon>
);
export const PlayIcon = () => (
  <Icon size={20}>
    <path
      d="M8 5.6v12.8a.8.8 0 0 0 1.2.7l10-6.4a.8.8 0 0 0 0-1.4l-10-6.4A.8.8 0 0 0 8 5.6z"
      {...solid}
    />
  </Icon>
);
export const PauseIcon = () => (
  <Icon size={20}>
    <rect x="6" y="5" width="4" height="14" rx="1" {...solid} />
    <rect x="14" y="5" width="4" height="14" rx="1" {...solid} />
  </Icon>
);
export const StopIcon = () => (
  <Icon size={20}>
    <rect x="6" y="6" width="12" height="12" rx="2" {...solid} />
  </Icon>
);
export const SidebarIcon = () => (
  <Icon size={20}>
    <rect x="3" y="4" width="18" height="16" rx="2.5" />
    <path d="M9 4v16" />
  </Icon>
);
export const KeyboardIcon = () => (
  <Icon size={20}>
    <rect x="2.5" y="6" width="19" height="12" rx="2.5" />
    <path d="M6.5 10h.01M10 10h.01M14 10h.01M17.5 10h.01M8 14h8" />
  </Icon>
);
export const CloseIcon = () => (
  <Icon size={20}>
    <path d="m6 6 12 12M18 6 6 18" />
  </Icon>
);
export const VolumeIcon = () => (
  <Icon size={20}>
    <path d="M4 9.5h3.5L12 6v12l-4.5-3.5H4z" {...solid} />
    <path d="M15.5 9.2a4 4 0 0 1 0 5.6M18 6.7a7.5 7.5 0 0 1 0 10.6" />
  </Icon>
);
export const MutedIcon = () => (
  <Icon size={20}>
    <path d="M4 9.5h3.5L12 6v12l-4.5-3.5H4z" {...solid} />
    <path d="m16 9.5 5 5m0-5-5 5" />
  </Icon>
);
export const FullscreenIcon = () => (
  <Icon size={20}>
    <path d="M4 9V5.5A1.5 1.5 0 0 1 5.5 4H9M15 4h3.5A1.5 1.5 0 0 1 20 5.5V9M20 15v3.5a1.5 1.5 0 0 1-1.5 1.5H15M9 20H5.5A1.5 1.5 0 0 1 4 18.5V15" />
  </Icon>
);
export const ExitFullscreenIcon = () => (
  <Icon size={20}>
    <path d="M9 4v3.5A1.5 1.5 0 0 1 7.5 9H4M20 9h-3.5A1.5 1.5 0 0 1 15 7.5V4M15 20v-3.5a1.5 1.5 0 0 1 1.5-1.5H20M4 15h3.5A1.5 1.5 0 0 1 9 16.5V20" />
  </Icon>
);
export const PipIcon = () => (
  <Icon size={20}>
    <rect x="3" y="5" width="18" height="14" rx="2.5" />
    <rect x="12" y="11.5" width="7" height="5.5" rx="1.2" {...solid} />
  </Icon>
);
