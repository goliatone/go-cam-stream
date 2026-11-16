// @vitest-environment node
import { crc32, deflateSync } from "node:zlib";
import { describe, expect, it } from "vitest";
import { OVER, WITHIN, compareImages, decodePng, encodePng } from "./snapshot-png.mjs";

/** A width × height RGBA image whose pixels come from `paint(x, y)`. */
function image(width, height, paint) {
  const pixels = new Uint8Array(width * height * 4);
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) pixels.set(paint(x, y), (y * width + x) * 4);
  return { width, height, pixels };
}

const gradient = (x, y) => [x * 40, y * 90, (x * 37 + y * 53) % 256, 255 - x * y];

function chunk(type, body) {
  const out = Buffer.alloc(12 + body.length);
  out.writeUInt32BE(body.length, 0);
  out.write(type, 4, "latin1");
  body.copy(out, 8);
  out.writeUInt32BE(crc32(out.subarray(4, 8 + body.length)), 8 + body.length);
  return out;
}

/** A PNG with the given colour type, each row stored with the given filter. */
function png(width, height, colour, rows, filters, header = {}) {
  const channels = colour === 6 ? 4 : 3;
  const stride = width * channels;
  const raw = Buffer.alloc(height * (stride + 1));
  let previous = new Uint8Array(stride);
  rows.forEach((row, y) => {
    const filter = filters[y % filters.length];
    raw[y * (stride + 1)] = filter;
    for (let i = 0; i < stride; i++) {
      const left = i >= channels ? row[i - channels] : 0;
      const up = previous[i];
      const upLeft = i >= channels ? previous[i - channels] : 0;
      const estimate = left + up - upLeft;
      const [toLeft, toUp, toUpLeft] = [left, up, upLeft].map((v) =>
        Math.abs(estimate - v),
      );
      const paeth =
        toLeft <= toUp && toLeft <= toUpLeft ? left : toUp <= toUpLeft ? up : upLeft;
      const predicted = [0, left, up, (left + up) >> 1, paeth][filter];
      raw[y * (stride + 1) + 1 + i] = (row[i] - predicted) & 255;
    }
    previous = row;
  });
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = header.depth ?? 8;
  ihdr[9] = colour;
  ihdr[12] = header.interlace ?? 0;
  const data = deflateSync(raw);
  return Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    chunk("IHDR", ihdr),
    // Image data may be split across chunks.
    chunk("IDAT", data.subarray(0, 5)),
    chunk("IDAT", data.subarray(5)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

describe("snapshot PNG files", () => {
  it("encode RGBA pixels that decode to the same pixels", () => {
    const source = image(5, 4, gradient);
    const decoded = decodePng(encodePng(source));
    expect(decoded.width).toBe(5);
    expect(decoded.height).toBe(4);
    expect(Buffer.from(decoded.pixels).equals(Buffer.from(source.pixels))).toBe(true);
  });

  it.each([
    ["RGBA", 6],
    ["RGB", 2],
  ])("decode %s rows stored with each filter", (_, colour) => {
    const channels = colour === 6 ? 4 : 3;
    const source = image(7, 10, gradient);
    const rows = Array.from({ length: 10 }, (_, y) =>
      Uint8Array.from(
        Array.from({ length: 7 }, (_, x) =>
          gradient(x, y).slice(0, channels),
        ).flat(),
      ),
    );
    const decoded = decodePng(png(7, 10, colour, rows, [0, 1, 2, 3, 4]));
    const expected = image(7, 10, (x, y) => [
      ...gradient(x, y).slice(0, 3),
      channels === 4 ? gradient(x, y)[3] : 255,
    ]);
    expect(decoded.width).toBe(source.width);
    expect(Buffer.from(decoded.pixels).equals(Buffer.from(expected.pixels))).toBe(
      true,
    );
  });

  it("refuse files they cannot read", () => {
    expect(() => decodePng(Buffer.from("not a png"))).toThrow("Not a PNG");
    const row = [Uint8Array.of(1, 2, 3)];
    expect(() => decodePng(png(1, 1, 3, row, [0]))).toThrow("colour type 3");
    expect(() => decodePng(png(1, 1, 2, row, [0], { depth: 16 }))).toThrow(
      "bit depth 16",
    );
    expect(() => decodePng(png(1, 1, 2, row, [0], { interlace: 1 }))).toThrow(
      "interlace 1",
    );
  });
});

describe("comparing snapshots", () => {
  const base = image(6, 5, gradient);
  const pixel = (result, x, y) => {
    const at = (y * result.width + x) * 4;
    return [...result.image.pixels.subarray(at, at + 4)];
  };

  it("find no difference between equal images", () => {
    const result = compareImages(base, image(6, 5, gradient));
    expect(result).toMatchObject({ differing: 0, over: 0, maxDelta: 0, box: null });
  });

  it("count differing pixels, the largest difference and where they are", () => {
    const head = image(6, 5, (x, y) =>
      x === 4 && y === 1 ? [0, 0, 0, 255] : x === 1 && y === 3 ? [...gradient(x, y).slice(0, 3), 250] : gradient(x, y),
    );
    const result = compareImages(base, head);
    expect(result).toMatchObject({ differing: 2, over: 2 });
    expect(result.maxDelta).toBe(Math.max(...gradient(4, 1).slice(0, 3)));
    expect(result.box).toEqual({ left: 1, top: 1, right: 4, bottom: 3 });
    // Pixels over the tolerance are red, and the rest is a faded grey.
    expect(pixel(result, 4, 1)).toEqual([...OVER, 255]);
    const [r, g, b, a] = pixel(result, 0, 0);
    expect(r).toBe(g);
    expect(g).toBe(b);
    expect(r).toBeGreaterThanOrEqual(191);
    expect(a).toBe(255);
  });

  it("count differences within the tolerance apart, and show them in amber", () => {
    const head = image(6, 5, (x, y) =>
      gradient(x, y).map((v, c) => (c === 0 ? v + (x === 5 && y === 4 ? 9 : 2) : v)),
    );
    const within = compareImages(base, head, 2);
    expect(within).toMatchObject({ differing: 30, over: 1, maxDelta: 9 });
    expect(within.box).toEqual({ left: 0, top: 0, right: 5, bottom: 4 });
    expect(pixel(within, 0, 0)).toEqual([...WITHIN, 255]);
    expect(pixel(within, 5, 4)).toEqual([...OVER, 255]);
    expect(compareImages(base, head, 1)).toMatchObject({ differing: 30, over: 30 });
    expect(compareImages(base, head, 9)).toMatchObject({ differing: 30, over: 0 });
  });

  it("count pixels that only one image has as over the tolerance", () => {
    const taller = image(6, 7, gradient);
    const result = compareImages(base, taller, 254);
    expect(result).toMatchObject({ width: 6, height: 7, differing: 12, over: 12, maxDelta: 255 });
    expect(result.box).toEqual({ left: 0, top: 5, right: 5, bottom: 6 });
    expect(compareImages(taller, base, 254)).toMatchObject({ differing: 12, over: 12 });
  });
});
