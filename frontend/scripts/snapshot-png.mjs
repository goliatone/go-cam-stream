// Reads, writes and compares the PNG screenshots of the preview snapshots, with
// Node's own zlib.
import { crc32, deflateSync, inflateSync } from "node:zlib";

const SIGNATURE = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);

/**
 * Decodes an 8-bit, non-interlaced RGB or RGBA PNG, as Chrome's screenshots are,
 * to RGBA pixels.
 */
export function decodePng(file) {
  if (!file.subarray(0, 8).equals(SIGNATURE)) throw new Error("Not a PNG file");
  let width = 0;
  let height = 0;
  let channels = 0;
  const data = [];
  for (let at = 8; at + 8 <= file.length; ) {
    const length = file.readUInt32BE(at);
    const type = file.toString("latin1", at + 4, at + 8);
    const body = file.subarray(at + 8, at + 8 + length);
    if (type === "IHDR") {
      width = body.readUInt32BE(0);
      height = body.readUInt32BE(4);
      const [depth, colour, , , interlace] = body.subarray(8, 13);
      if (depth !== 8 || (colour !== 2 && colour !== 6) || interlace !== 0)
        throw new Error(
          `Unsupported PNG: bit depth ${depth}, colour type ${colour}, interlace ${interlace}`,
        );
      channels = colour === 6 ? 4 : 3;
    } else if (type === "IDAT") data.push(body);
    else if (type === "IEND") break;
    at += 12 + length;
  }
  if (!channels) throw new Error("PNG has no header");
  const stride = width * channels;
  const raw = inflateSync(Buffer.concat(data));
  if (raw.length !== height * (stride + 1))
    throw new Error("PNG image data has the wrong length");
  const pixels = new Uint8Array(width * height * 4);
  let previous = new Uint8Array(stride);
  for (let y = 0; y < height; y++) {
    const start = y * (stride + 1);
    const filter = raw[start];
    const row = new Uint8Array(stride);
    for (let i = 0; i < stride; i++) {
      const left = i >= channels ? row[i - channels] : 0;
      const upLeft = i >= channels ? previous[i - channels] : 0;
      row[i] = raw[start + 1 + i] + predict(filter, left, previous[i], upLeft);
    }
    if (channels === 4) pixels.set(row, y * width * 4);
    else
      for (let x = 0, to = y * width * 4; x < width; x++, to += 4) {
        pixels[to] = row[x * 3];
        pixels[to + 1] = row[x * 3 + 1];
        pixels[to + 2] = row[x * 3 + 2];
        pixels[to + 3] = 255;
      }
    previous = row;
  }
  return { width, height, pixels };
}

/** PNG's filter predictors, for the byte to the left, above and above left. */
function predict(filter, left, up, upLeft) {
  switch (filter) {
    case 0:
      return 0;
    case 1:
      return left;
    case 2:
      return up;
    case 3:
      return (left + up) >> 1;
    case 4: {
      const estimate = left + up - upLeft;
      const toLeft = Math.abs(estimate - left);
      const toUp = Math.abs(estimate - up);
      const toUpLeft = Math.abs(estimate - upLeft);
      return toLeft <= toUp && toLeft <= toUpLeft
        ? left
        : toUp <= toUpLeft
          ? up
          : upLeft;
    }
    default:
      throw new Error(`Unknown PNG filter ${filter}`);
  }
}

/** Encodes RGBA pixels as an 8-bit RGBA PNG. */
export function encodePng({ width, height, pixels }) {
  const stride = width * 4;
  const raw = Buffer.alloc(height * (stride + 1));
  for (let y = 0; y < height; y++)
    raw.set(pixels.subarray(y * stride, (y + 1) * stride), y * (stride + 1) + 1);
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header[8] = 8;
  header[9] = 6;
  return Buffer.concat([
    SIGNATURE,
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(raw)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

function chunk(type, body) {
  const out = Buffer.alloc(12 + body.length);
  out.writeUInt32BE(body.length, 0);
  out.write(type, 4, "latin1");
  body.copy(out, 8);
  out.writeUInt32BE(crc32(out.subarray(4, 8 + body.length)), 8 + body.length);
  return out;
}

/** The diff image's colours for a pixel over the tolerance and one within it. */
export const OVER = [230, 0, 60];
export const WITHIN = [245, 160, 0];

/**
 * Compares two images pixel by pixel. A pixel differs when any channel differs,
 * and is over the tolerance when a channel differs by more than `tolerance`
 * levels; a pixel that only one image has is over it. Returns both counts, the
 * largest difference, the box around the pixels that differ and an image that
 * shows the pixels over the tolerance in red and the others that differ in amber,
 * over a faded copy of `head`.
 */
export function compareImages(base, head, tolerance = 0) {
  const width = Math.max(base.width, head.width);
  const height = Math.max(base.height, head.height);
  const image = new Uint8Array(width * height * 4);
  let differing = 0;
  let over = 0;
  let maxDelta = 0;
  let left = width;
  let top = height;
  let right = -1;
  let bottom = -1;
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) {
      const inBase = x < base.width && y < base.height;
      const inHead = x < head.width && y < head.height;
      const a = (y * base.width + x) * 4;
      const b = (y * head.width + x) * 4;
      let delta = 255;
      if (inBase && inHead) {
        delta = 0;
        for (let c = 0; c < 4; c++)
          delta = Math.max(delta, Math.abs(base.pixels[a + c] - head.pixels[b + c]));
      }
      const to = (y * width + x) * 4;
      image[to + 3] = 255;
      if (delta) {
        differing++;
        maxDelta = Math.max(maxDelta, delta);
        left = Math.min(left, x);
        top = Math.min(top, y);
        right = Math.max(right, x);
        bottom = Math.max(bottom, y);
      }
      if (delta > tolerance) {
        over++;
        image.set(OVER, to);
      } else if (delta) image.set(WITHIN, to);
      else {
        const p = head.pixels;
        const grey = 0.299 * p[b] + 0.587 * p[b + 1] + 0.114 * p[b + 2];
        image.fill(Math.round(255 - (255 - grey) * 0.25), to, to + 3);
      }
    }
  return {
    width,
    height,
    differing,
    over,
    maxDelta,
    box: differing ? { left, top, right, bottom } : null,
    image: { width, height, pixels: image },
  };
}
