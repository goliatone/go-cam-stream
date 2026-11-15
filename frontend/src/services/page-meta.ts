import type { Tone } from "../components/status";

const BRAND = "camstream";
const DOT_COLORS: Partial<Record<Tone, string>> = {
  live: "#1e9e5a",
  busy: "#d4900f",
  danger: "#d23f3f",
};

/** Inline SVG favicon with an optional state dot. The production CSP allows data: images. */
export function faviconHref(tone: Tone | null) {
  const color = tone ? DOT_COLORS[tone] : undefined;
  const dot = color
    ? `<circle cx='25' cy='8' r='6' fill='${color}' stroke='white' stroke-width='2'/>`
    : "";
  const svg = `<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'><rect x='1' y='8' width='21' height='17' rx='5' fill='#176b51'/><path d='M22 14.5 30 10v13l-8-4.5z' fill='#176b51'/><circle cx='11.5' cy='16.5' r='4' fill='white'/>${dot}</svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

/** "Hall · Live · camstream"; parts that are unknown are left out. */
export function pageTitle(camera?: string, state?: string) {
  return [camera, state, BRAND].filter(Boolean).join(" · ");
}

/** Shows the view in the browser tab, so several open tabs stay distinguishable. */
export function showPageState(title: string, tone: Tone | null) {
  document.title = title;
  let icon = document.head.querySelector<HTMLLinkElement>('link[rel~="icon"]');
  if (!icon) {
    icon = document.createElement("link");
    icon.rel = "icon";
    document.head.append(icon);
  }
  const href = faviconHref(tone);
  if (icon.getAttribute("href") !== href) icon.setAttribute("href", href);
}
