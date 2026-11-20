import type { JSX } from "preact";

// Inline geometry: the page's policy allows no image requests. Shapes are
// functions so every icon renders its own nodes.
const shapes = {
  mic: () => <><rect x="9" y="3" width="6" height="11" rx="3" /><path d="M5.5 11a6.5 6.5 0 0 0 13 0M12 17.5V21M8.5 21h7" /></>,
  "mic-off": () => <path d="M15 9.4V6a3 3 0 0 0-5.6-1.5M9 9v2a3 3 0 0 0 4.7 2.5M18.5 11a6.5 6.5 0 0 1-.8 3.1M5.5 11a6.5 6.5 0 0 0 10.6 5M12 17.5V21M8.5 21h7M3.5 3.5l17 17" />,
  camera: () => <><rect x="2.5" y="6" width="13" height="12" rx="2.5" /><path d="M15.5 10.2 21 7v10l-5.5-3.2" /></>,
  "camera-off": () => <path d="M10.5 6H13a2.5 2.5 0 0 1 2.5 2.5V11M15.5 15.5A2.5 2.5 0 0 1 13 18H5a2.5 2.5 0 0 1-2.5-2.5v-7A2.5 2.5 0 0 1 4.8 6M15.5 10.2 21 7v10l-3.4-2M3.5 3.5l17 17" />,
  "chev-up": () => <path d="m7 14.5 5-5 5 5" />,
  "chev-down": () => <path d="m7 9.5 5 5 5-5" />,
  more: () => <><circle cx="5.5" cy="12" r="1.5" fill="currentColor" /><circle cx="12" cy="12" r="1.5" fill="currentColor" /><circle cx="18.5" cy="12" r="1.5" fill="currentColor" /></>,
  end: () => <path class="solid" d="M3 14.5c5-4.5 13-4.5 18 0l-1.8 3.2-4.2-1.2v-2.3a9.5 9.5 0 0 0-6 0v2.3l-4.2 1.2z" />,
  sound: () => <><path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z" /><path d="M15.5 9a4 4 0 0 1 0 6M18.2 6.5a8 8 0 0 1 0 11" /></>,
  "sound-off": () => <><path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z" /><path d="m16 9.5 5 5M21 9.5l-5 5" /></>,
  swap: () => <path d="M4 8h13.5M14 4.5 17.5 8 14 11.5M20 16H6.5M10 12.5 6.5 16l3.5 3.5" />,
  collapse: () => <path d="M4 14h6v6M20 10h-6V4M14 10l6.5-6.5M3.5 20.5 10 14" />,
  expand: () => <path d="M15 4h5v5M20 4l-6 6M9 20H4v-5M4 20l6-6" />,
  copy: () => <><rect x="8.5" y="8.5" width="11.5" height="11.5" rx="2.5" /><path d="M15.5 8.5V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v7.5a2 2 0 0 0 2 2h2.5" /></>,
  link: () => <path d="M10 14a4.5 4.5 0 0 0 6.4 0l3-3a4.5 4.5 0 0 0-6.4-6.4l-1 1M14 10a4.5 4.5 0 0 0-6.4 0l-3 3a4.5 4.5 0 0 0 6.4 6.4l1-1" />,
  check: () => <path d="m5 12.5 4.5 4.5L19 7.5" />,
  alert: () => <><path d="M10.3 4.6 2.9 17.5a2 2 0 0 0 1.7 3h14.8a2 2 0 0 0 1.7-3L13.7 4.6a2 2 0 0 0-3.4 0z" /><path d="M12 9.5v4M12 17h.01" /></>,
  close: () => <path d="M6 6l12 12M18 6 6 18" />,
  eye: () => <><path d="M2.5 12C3.5 10.2 7 6 12 6s8.5 4.2 9.5 6c-1 1.8-4.5 6-9.5 6s-8.5-4.2-9.5-6z" /><circle cx="12" cy="12" r="3" /></>,
  "eye-off": () => <path d="M3.5 3.5l17 17M10.6 6.1A9.6 9.6 0 0 1 12 6c5 0 8.5 4.2 9.5 6-.5.9-1.4 2.2-2.7 3.4M6.4 7.7C4.5 9 3.1 10.9 2.5 12c1 1.8 4.5 6 9.5 6 1.6 0 3-.4 4.3-1M9.9 9.9a3 3 0 0 0 4.2 4.2" />,
  play: () => <path class="solid" d="M8 5.5v13l10.5-6.5z" />,
  user: () => <><circle cx="12" cy="8.5" r="3.8" /><path d="M4.8 20c1.1-3.6 3.9-5.5 7.2-5.5s6.1 1.9 7.2 5.5" /></>,
  clock: () => <><circle cx="12" cy="12" r="8.5" /><path d="M12 7.5V12l3 2" /></>,
  leave: () => <path d="M14 4h4a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-4M10 16l-4-4 4-4M6 12h10" />,
  signal: () => <path d="M2 9a14.5 14.5 0 0 1 20 0M5 12.5a10 10 0 0 1 14 0M8.5 16a5 5 0 0 1 7 0M12 19.5h.01" />,
} satisfies Record<string, () => JSX.Element>;

export type IconName = keyof typeof shapes;

/** A decorative icon, or an image with a name when `label` is given. */
export function Icon({ name, label, class: extra }: { name: IconName; label?: string; class?: string }) {
  const Shape = shapes[name];
  const naming = label ? { role: "img" as const, "aria-label": label } : { "aria-hidden": true as const };
  return <svg class={extra ? `icon ${extra}` : "icon"} viewBox="0 0 24 24" focusable="false" {...naming}><Shape /></svg>;
}
