// Canvas-drawn camera scene streamed into a real <video> element, so play,
// pause, picture in picture and fullscreen behave like a live feed.
// Timers rather than animation frames keep picture in picture updating
// (throttled) when the tab is in the background.
import type { Look } from "./catalog";

export interface FeedOptions extends Look {
  name: string;
  width: number;
  height: number;
}
export interface Feed {
  /** null when the browser cannot capture a canvas; show the canvas instead. */
  readonly stream: MediaStream | null;
  /** Frozen feeds keep showing their last frame (reconnecting). */
  setFrozen(frozen: boolean): void;
  dispose(): void;
}

const FPS = 30;

export function startFeed(canvas: HTMLCanvasElement, options: FeedOptions): Feed {
  canvas.width = options.width;
  canvas.height = options.height;
  const ctx = canvas.getContext("2d");
  const stream =
    "captureStream" in canvas && typeof canvas.captureStream === "function"
      ? canvas.captureStream(FPS)
      : null;
  const started = performance.now();
  let frozen = false;
  const draw = () => {
    if (ctx && !frozen) paint(ctx, options, performance.now() - started);
  };
  draw();
  const timer = setInterval(draw, 1000 / FPS);
  return {
    stream,
    setFrozen(value) {
      frozen = value;
    },
    dispose() {
      clearInterval(timer);
      stream?.getTracks().forEach((track) => track.stop());
    },
  };
}

function paint(ctx: CanvasRenderingContext2D, o: FeedOptions, t: number) {
  const { width: w, height: h } = o;
  const s = w / 1280;
  if (o.scene === "outdoor") outdoor(ctx, w, h, s, o.hue, t);
  else room(ctx, w, h, s, o.hue, t);
  overlay(ctx, w, s, o.name);
}

function room(
  ctx: CanvasRenderingContext2D,
  w: number,
  h: number,
  s: number,
  hue: number,
  t: number,
) {
  const wall = ctx.createLinearGradient(0, 0, 0, h);
  wall.addColorStop(0, `hsl(${hue} 22% 30%)`);
  wall.addColorStop(0.72, `hsl(${hue} 18% 19%)`);
  ctx.fillStyle = wall;
  ctx.fillRect(0, 0, w, h);

  // Window with slow drifting daylight.
  const wx = w * 0.6;
  const wy = h * 0.13;
  const ww = w * 0.25;
  const wh = h * 0.4;
  const sky = ctx.createLinearGradient(0, wy, 0, wy + wh);
  sky.addColorStop(0, `hsl(${hue + 175} 42% 70%)`);
  sky.addColorStop(1, `hsl(${hue + 150} 38% 84%)`);
  ctx.fillStyle = sky;
  ctx.fillRect(wx, wy, ww, wh);
  ctx.fillStyle = "rgba(255,255,255,.28)";
  const cloud = wx + ((t / 90) % (ww + 120 * s)) - 60 * s;
  ellipse(ctx, cloud, wy + wh * 0.32, 46 * s, 14 * s);
  ctx.strokeStyle = `hsl(${hue} 16% 13%)`;
  ctx.lineWidth = 9 * s;
  ctx.strokeRect(wx, wy, ww, wh);
  ctx.beginPath();
  ctx.moveTo(wx + ww / 2, wy);
  ctx.lineTo(wx + ww / 2, wy + wh);
  ctx.stroke();

  // Floor and furniture.
  ctx.fillStyle = `hsl(${hue} 15% 12%)`;
  ctx.beginPath();
  ctx.moveTo(0, h * 0.74);
  ctx.lineTo(w, h * 0.7);
  ctx.lineTo(w, h);
  ctx.lineTo(0, h);
  ctx.closePath();
  ctx.fill();
  ctx.fillStyle = `hsl(${hue} 20% 24%)`;
  roundRect(ctx, w * 0.07, h * 0.5, w * 0.34, h * 0.25, 22 * s);
  ctx.fillStyle = `hsl(${hue} 22% 30%)`;
  roundRect(ctx, w * 0.09, h * 0.45, w * 0.3, h * 0.12, 18 * s);

  // A lamp glow sweeping the wall.
  const gx = w * (0.35 + 0.18 * Math.sin(t / 5200));
  const glow = ctx.createRadialGradient(gx, h * 0.3, 0, gx, h * 0.3, w * 0.42);
  glow.addColorStop(0, `hsla(${hue + 20} 85% 70% / .16)`);
  glow.addColorStop(1, "hsla(0 0% 0% / 0)");
  ctx.fillStyle = glow;
  ctx.fillRect(0, 0, w, h);

  // Something moving across the floor.
  const period = 16_000;
  const p = (t % period) / period;
  const leg = p < 0.5 ? p * 2 : (1 - p) * 2;
  const x = w * (0.18 + 0.64 * ease(leg));
  const y = h * 0.84 + Math.sin(t / 170) * 2.5 * s;
  const dir = p < 0.5 ? 1 : -1;
  ctx.fillStyle = "rgba(0,0,0,.28)";
  ellipse(ctx, x, h * 0.9, 70 * s, 10 * s);
  ctx.fillStyle = `hsl(${hue + 200} 14% 72%)`;
  ellipse(ctx, x, y, 62 * s, 30 * s);
  ellipse(ctx, x + dir * 58 * s, y - 24 * s, 24 * s, 22 * s);
}

function outdoor(
  ctx: CanvasRenderingContext2D,
  w: number,
  h: number,
  s: number,
  hue: number,
  t: number,
) {
  const sky = ctx.createLinearGradient(0, 0, 0, h * 0.62);
  sky.addColorStop(0, `hsl(${hue} 55% 62%)`);
  sky.addColorStop(1, `hsl(${hue - 20} 45% 82%)`);
  ctx.fillStyle = sky;
  ctx.fillRect(0, 0, w, h);
  ctx.fillStyle = "rgba(255,255,255,.7)";
  const drift = (t / 60) % (w + 300 * s);
  ellipse(ctx, drift - 150 * s, h * 0.18, 90 * s, 24 * s);
  ellipse(ctx, drift - 80 * s, h * 0.15, 60 * s, 20 * s);
  ctx.fillStyle = `hsl(${hue + 0} 18% 46%)`;
  ctx.beginPath();
  ctx.moveTo(0, h * 0.56);
  ctx.quadraticCurveTo(w * 0.3, h * 0.44, w * 0.6, h * 0.55);
  ctx.quadraticCurveTo(w * 0.82, h * 0.6, w, h * 0.5);
  ctx.lineTo(w, h);
  ctx.lineTo(0, h);
  ctx.fill();
  ctx.fillStyle = "hsl(110 22% 30%)";
  ctx.fillRect(0, h * 0.62, w, h * 0.38);
  ctx.fillStyle = "hsl(35 20% 58%)";
  ctx.beginPath();
  ctx.moveTo(w * 0.44, h);
  ctx.lineTo(w * 0.5, h * 0.62);
  ctx.lineTo(w * 0.56, h * 0.62);
  ctx.lineTo(w * 0.7, h);
  ctx.fill();
  // A tree swaying in the wind.
  const sway = Math.sin(t / 900) * 8 * s;
  ctx.fillStyle = "hsl(25 30% 22%)";
  ctx.fillRect(w * 0.18, h * 0.38, 18 * s, h * 0.3);
  ctx.fillStyle = "hsl(120 28% 28%)";
  ellipse(ctx, w * 0.18 + 9 * s + sway, h * 0.34, 92 * s, 80 * s);
  ctx.fillStyle = "hsl(120 30% 34%)";
  ellipse(ctx, w * 0.18 + 40 * s + sway * 1.3, h * 0.3, 58 * s, 50 * s);
}

function overlay(
  ctx: CanvasRenderingContext2D,
  w: number,
  s: number,
  name: string,
) {
  const now = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  const stamp = `${now.getFullYear()}-${p(now.getMonth() + 1)}-${p(now.getDate())} ${p(now.getHours())}:${p(now.getMinutes())}:${p(now.getSeconds())}`;
  ctx.save();
  ctx.textAlign = "right";
  ctx.textBaseline = "top";
  ctx.shadowColor = "rgba(0,0,0,.65)";
  ctx.shadowBlur = 4 * s;
  ctx.fillStyle = "rgba(255,255,255,.92)";
  ctx.font = `600 ${Math.round(22 * s)}px ui-monospace, Menlo, Consolas, monospace`;
  ctx.fillText(stamp, w - 28 * s, 24 * s);
  ctx.fillStyle = "rgba(255,255,255,.7)";
  ctx.font = `600 ${Math.round(16 * s)}px ui-monospace, Menlo, Consolas, monospace`;
  ctx.fillText(`${name.toUpperCase()} · SYNTHETIC`, w - 28 * s, 56 * s);
  ctx.restore();
}

function ellipse(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  rx: number,
  ry: number,
) {
  ctx.beginPath();
  ctx.ellipse(x, y, rx, ry, 0, 0, Math.PI * 2);
  ctx.fill();
}

function roundRect(
  ctx: CanvasRenderingContext2D,
  x: number,
  y: number,
  w: number,
  h: number,
  r: number,
) {
  ctx.beginPath();
  if (typeof ctx.roundRect === "function") ctx.roundRect(x, y, w, h, r);
  else ctx.rect(x, y, w, h);
  ctx.fill();
}

function ease(x: number) {
  return x < 0.5 ? 2 * x * x : 1 - (-2 * x + 2) ** 2 / 2;
}
