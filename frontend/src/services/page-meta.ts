import type { Tone } from "../components/status";

import idle from "../components/favicon-idle.svg?no-inline";
import live from "../components/favicon-live.svg?no-inline";
import busy from "../components/favicon-busy.svg?no-inline";
import danger from "../components/favicon-danger.svg?no-inline";
const BRAND = "camstream";
const ICONS: Partial<Record<Tone, string>> = { live, busy, danger };
/** Hashed same-origin favicons share the viewer asset admission boundary. */
export function faviconHref(tone: Tone | null) { return (tone && ICONS[tone]) || idle; }

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
