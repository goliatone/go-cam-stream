import { describe, expect, it } from "vitest";
import css from "./styles.css?raw";

// The contrast audit of the viewer's tokens in both themes, at WCAG 2.2 AA: 4.5:1
// for text, and 3:1 for icons that identify a control and for focus rings. What
// sits on the video is checked over a white frame, the worst case for its light
// text and icons. Status dots are left out: each sits beside its status text.
const TEXT = 4.5;
const GRAPHIC = 3;

type RGBA = readonly [number, number, number, number];
type Declaration = readonly [property: string, value: string];
/** A style rule, with the at-rules around it, such as `@container (…)`. */
type Rule = { at: string; selector: string; declarations: Declaration[] };

/** One space between words, and none inside parentheses. */
const squash = (text: string) =>
  text.replace(/\s+/g, " ").replace(/\( /g, "(").replace(/ \)/g, ")").trim();

function parseDeclarations(body: string): Declaration[] {
  const declarations: Declaration[] = [];
  for (const part of body.split(";")) {
    const match = /^\s*([\w-]+)\s*:([\s\S]*)$/.exec(part);
    if (!match) continue;
    const name = match[1]!;
    declarations.push([
      name.startsWith("--") ? name : name.toLowerCase(),
      squash(match[2]!),
    ]);
  }
  return declarations;
}

/** Every style rule. The stylesheet nests rules only in at-rules. */
function parseRules(text: string): Rule[] {
  const rules: Rule[] = [];
  const open: string[] = [];
  let start = 0;
  for (let i = 0; i < text.length; i++) {
    if (text[i] === "{") {
      open.push(squash(text.slice(start, i)));
      start = i + 1;
    } else if (text[i] === "}") {
      const head = open.pop() ?? "";
      if (!head.startsWith("@"))
        rules.push({
          at: open.join(" "),
          selector: head,
          declarations: parseDeclarations(text.slice(start, i)),
        });
      start = i + 1;
    }
  }
  return rules;
}

/** Splits at commas, spaces or combinators outside brackets and parentheses. */
function splitTop(text: string, at: RegExp) {
  const parts: string[] = [];
  let depth = 0;
  let part = "";
  for (const c of text) {
    if ("([".includes(c)) depth++;
    if (")]".includes(c)) depth--;
    if (depth === 0 && at.test(c)) {
      parts.push(part);
      part = "";
    } else part += c;
  }
  parts.push(part);
  return parts.map((one) => one.trim()).filter(Boolean);
}
/** What each selector in a list styles, such as `.b:hover` in `.a > .b:hover`. */
const subjects = (selector: string) =>
  splitTop(selector, /,/).map((one) => splitTop(one, /[\s>+~]/).at(-1)!);

const source = css.replace(/\/\*[\s\S]*?\*\//g, "");
const rules = parseRules(source);
/** The top-level rule, or the one inside `at`, with exactly this selector. */
const rule = (selector: string, at = "") =>
  rules.find((r) => r.selector === selector && r.at === at);
/** The value a rule gives a property, the last if it repeats. */
const get = (r: Rule | undefined, property: string) =>
  r?.declarations.filter(([name]) => name === property).at(-1)?.[1];

// The tokens are the first `:root` rule. The dialog backdrop's token is set on
// `::backdrop`, which inherits no custom properties in older browsers. When the
// system prefers dark, a `:root` rule in this media query replaces page tokens.
const DARK = "@media (prefers-color-scheme: dark)";
const tokenRule = rule(":root");
const backdropRule = rule("::backdrop");
const darkRule = rule(":root", DARK);
const TOKEN_RULES = [tokenRule, backdropRule, darkRule];
type Tokens = ReadonlyMap<string, string>;
const light: Tokens = new Map(
  [tokenRule, backdropRule].flatMap((r) => r?.declarations ?? []),
);
const dark: Tokens = new Map([...light, ...(darkRule?.declarations ?? [])]);
const THEMES = [
  ["light", light],
  ["dark", dark],
] as const;
const styled = rules.filter((r) => !TOKEN_RULES.includes(r));
/** A value with each token replaced by its light definition. */
const resolve = (value: string) =>
  value.replace(/var\((--[\w-]+)\)/g, (_, name: string) => light.get(name) ?? "");

function parse(value: string): RGBA {
  const hex = /^#([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})$/i.exec(value)?.[1];
  if (hex) {
    const [r, g, b, a = 255] = (hex.length < 6 ? hex.replace(/./g, "$&$&") : hex)
      .match(/../g)!
      .map((pair) => parseInt(pair, 16));
    return [r!, g!, b!, a / 255];
  }
  const rgb =
    /^rgba?\(([\d.]+)[ ,]+([\d.]+)[ ,]+([\d.]+)(?: ?[/,] ?([\d.]+)(%?))?\)$/.exec(
      value,
    );
  if (!rgb) throw new Error(`Not a colour: ${value}`);
  const alpha = rgb[4] === undefined ? 1 : Number(rgb[4]) / (rgb[5] ? 100 : 1);
  return [Number(rgb[1]), Number(rgb[2]), Number(rgb[3]), alpha];
}
/** A token's colour in a theme. */
function colour(theme: Tokens, name: string): RGBA {
  const value = theme.get(name);
  if (value === undefined) throw new Error(`No token ${name}`);
  return parse(value);
}
/** Paints `top` over an opaque `bottom`. */
const over = ([r, g, b, a]: RGBA, [R, G, B]: RGBA): RGBA => [
  r * a + R * (1 - a),
  g * a + G * (1 - a),
  b * a + B * (1 - a),
  1,
];
const channel = (v: number) =>
  v / 255 <= 0.04045 ? v / 255 / 12.92 : ((v / 255 + 0.055) / 1.055) ** 2.4;
const luminance = ([r, g, b]: RGBA) =>
  0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
function contrast(foreground: RGBA, background: RGBA) {
  const a = luminance(over(foreground, background));
  const b = luminance(background);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

const WHITE_FRAME: RGBA = [255, 255, 255, 1];
const PAGE = ["--bg", "--surface", "--fill", "--fill-soft", "--accent-soft"];
type Pair = [what: string, colour: string, background: RGBA, needs: number];
/** What is drawn, its colour, what it is drawn on and the ratio it needs. */
function pairs(theme: Tokens): Pair[] {
  const token = (name: string) => colour(theme, name);
  /** The named layers painted in order over an opaque base. */
  const on = (base: RGBA | string, ...layers: string[]): RGBA =>
    layers.reduce<RGBA>(
      (below, layer) => over(token(layer), below),
      typeof base === "string" ? token(base) : base,
    );
  return [
    ...PAGE.flatMap((bg): Pair[] => [
      [`Text on ${bg}`, "--text", token(bg), TEXT],
      [`Muted text and top bar icons on ${bg}`, "--text-muted", token(bg), TEXT],
      [`Focus ring on ${bg}`, "--focus", token(bg), GRAPHIC],
    ]),
    ["Unplayable camera name", "--text-disabled", token("--surface"), TEXT],
    // A phone's camera list has no surface of its own.
    ["Unplayable camera name on a phone", "--text-disabled", token("--bg"), TEXT],
    ["Stale status beside the video", "--warn", token("--bg"), TEXT],
    ["Stale status on a phone", "--warn", token("--surface"), TEXT],
    ["Top bar icon on hover", "--text", token("--fill-strong"), GRAPHIC],
    // The camera icon on each tile, the collapsed rail's only content.
    ...[0, 1, 2, 3, 4, 5, 6, 7].map((n): Pair => [`Camera tile ${n}`, "--on-tile", token(`--tile-${n}`), GRAPHIC]),
    ["Copied link notice", "--surface", token("--text"), TEXT],
    // Buttons and fields on the page
    ["Primary button", "--on-accent", token("--accent"), TEXT],
    ["Primary button on hover", "--on-accent", token("--accent-hover"), TEXT],
    ["Secondary or ghost button on hover", "--text", token("--fill-strong"), TEXT],
    ["Danger button and field error", "--danger", token("--surface"), TEXT],
    ["Danger button on hover", "--danger", token("--danger-soft"), TEXT],
    ["Disabled button, field or choice", "--text-disabled", token("--fill"), TEXT],
    ["Add Camera", "--accent", token("--surface"), TEXT],
    ["Add Camera on hover", "--accent", token("--fill-soft"), TEXT],
    ["Chosen segment", "--accent", token("--accent-tint"), TEXT],
    // Adding a camera: the sign-in switch, the step bar and the checklist marks
    ["Switch track, off", "--text-muted", token("--surface"), GRAPHIC],
    ["Switch track, on, and current step", "--accent", token("--surface"), GRAPHIC],
    ["Switch thumb, on", "--surface", token("--accent"), GRAPHIC],
    ["Passed check", "--accent", token("--accent-tint"), GRAPHIC],
    ["Failed check", "--danger", token("--danger-soft"), GRAPHIC],
    ["Details and Where do I find these?", "--accent", token("--surface"), TEXT],
    ["Refresh icon on hover", "--warn", token("--fill-strong"), GRAPHIC],
    // Buttons on the stage
    ["Primary button on the stage", "--on-primary", token("--primary"), TEXT],
    ["Primary button on hover on the stage", "--on-primary", token("--primary-hover"), TEXT],
    ["Disabled primary button on the stage", "--on-disabled", token("--stage-hover"), TEXT],
    ["Secondary button on hover on the stage", "--on-stage", token("--stage-hover"), TEXT],
    ["Stage text", "--on-stage", token("--stage"), TEXT],
    ["Muted stage text", "--on-stage-muted", token("--stage"), TEXT],
    ["Status badge", "--on-stage-soft", on(WHITE_FRAME, "--glass"), TEXT],
    ["Cameras button", "--on-stage-strong", on(WHITE_FRAME, "--glass"), TEXT],
    ["Reconnecting notice", "--on-stage", on(WHITE_FRAME, "--scrim"), TEXT],
    ["Countdown", "--on-stage-muted", on(WHITE_FRAME, "--scrim"), TEXT],
    // A message's scrim is weakest at its edge.
    ["Message title", "--on-stage", on(WHITE_FRAME, "--scrim-edge"), TEXT],
    ["Message copy", "--on-stage-muted", on(WHITE_FRAME, "--scrim-edge"), TEXT],
    [
      "Secondary button on the stage",
      "--on-stage",
      on(WHITE_FRAME, "--scrim-edge", "--glass-weak"),
      TEXT,
    ],
    // A prompt keeps the frame in view and puts its copy on a backing.
    [
      "Prompt copy",
      "--on-stage-muted",
      on(WHITE_FRAME, "--scrim-light-edge", "--scrim"),
      TEXT,
    ],
    ["Hint and tooltip", "--on-stage-strong", on(WHITE_FRAME, "--tip"), TEXT],
    [
      "Toolbar icons",
      "--on-stage-strong",
      on(WHITE_FRAME, "--toolbar-shade"),
      GRAPHIC,
    ],
    // The white ring between its dark halo.
    ["Focus ring on video", "--on-stage-strong", on(WHITE_FRAME, "--tip"), GRAPHIC],
  ];
}

/** The backings that the pairs over a frame assume, as their rules declare them. */
const BACKINGS: [selector: string, property: string, value: string][] = [
  [
    ".video-stage.has-message::before",
    "background",
    "radial-gradient(ellipse at center, var(--scrim), var(--scrim-edge))",
  ],
  [
    ".video-stage.has-prompt::before",
    "background",
    "radial-gradient(ellipse at center, var(--scrim-light), var(--scrim-light-edge))",
  ],
  [".has-prompt .player-message", "background", "var(--scrim)"],
  [".stage-badge", "background", "var(--glass)"],
  [".overlay-cameras", "background", "var(--glass)"],
  [".player-notice", "background", "var(--scrim)"],
  [".video-stage .secondary-button", "background", "var(--glass-weak)"],
  [
    ".video-stage .secondary-button:hover:not(:disabled)",
    "background",
    "var(--stage-hover)",
  ],
  [".player-hint", "background", "var(--tip)"],
  [".control-button::after", "background", "var(--tip)"],
  [
    ".player-toolbar",
    "background",
    "linear-gradient(to top, var(--toolbar-shade) 62%, transparent)",
  ],
  [".video-stage :focus-visible", "outline-color", "var(--on-stage-strong)"],
  [".video-stage :focus-visible", "box-shadow", "0 0 0 7px var(--tip)"],
  // The skip link's ring lies on the link's own margin, also where it covers the
  // video in landscape.
  [".skip-link", "box-shadow", "0 0 0 6px var(--surface)"],
];
const BACKGROUND = /^background(?:-color|-image)?$/;
const OUTLINE = /^outline(?:-color)?$/;
/** Every rule that sets each backing, found by what its selector styles. */
const SETTERS: [styles: RegExp, property: RegExp, selectors: string[]][] = [
  [
    /\.(?:video-stage|has-message|has-prompt)(?![\w-]).*::before$/,
    BACKGROUND,
    [".video-stage.has-message::before", ".video-stage.has-prompt::before"],
  ],
  [/\.player-message(?![\w-])/, BACKGROUND, [".has-prompt .player-message"]],
  [/\.stage-badge(?![\w-])/, BACKGROUND, [".stage-badge"]],
  [/\.overlay-cameras(?![\w-])/, BACKGROUND, [".overlay-cameras"]],
  [/\.player-notice(?![\w-])/, BACKGROUND, [".player-notice"]],
  // The page's rules come first; on the stage, `.video-stage` outranks each.
  [
    /\.secondary-button(?![\w-])/,
    BACKGROUND,
    [
      ".secondary-button",
      ":is(.secondary-button, .ghost-button):hover:not(:disabled)",
      ":is(.primary-button, .secondary-button, .danger-button):disabled",
      ".video-stage .secondary-button",
      ".video-stage .secondary-button:hover:not(:disabled)",
    ],
  ],
  [/\.player-hint(?![\w-])/, BACKGROUND, [".player-hint"]],
  [/\.control-button(?![\w-]).*::after$/, BACKGROUND, [".control-button::after"]],
  [/\.player-toolbar(?![\w-])/, BACKGROUND, [".player-toolbar"]],
  [
    /:focus/,
    OUTLINE,
    [":focus-visible", "main:focus, .player:focus", ".video-stage :focus-visible"],
  ],
  [/:focus/, /^box-shadow$/, [".video-stage :focus-visible"]],
  [/\.skip-link(?![\w-])/, /^box-shadow$/, [".skip-link"]],
];

/** [ids, classes, types] of one selector. `:is()` and `:not()` count as their
    most specific argument, which is all this stylesheet's selectors need. */
function specificity(selector: string): number[] {
  const count = [0, 0, 0];
  const rest = selector.replace(/:(?:is|not)\(([^()]*)\)/g, (_, inner: string) => {
    const best = splitTop(inner, /,/)
      .map(specificity)
      .reduce((a, b) => (outranks(a, b) ? a : b));
    best.forEach((n, i) => (count[i]! += n));
    return "";
  });
  count[0]! += (rest.match(/#[\w-]+/g) ?? []).length;
  count[1]! += (rest.match(/\.[\w-]+|\[[^\]]*\]|(?<!:):[\w-]+/g) ?? []).length;
  count[2]! += (rest.match(/(?:^|[\s>+~])[a-z][\w-]*|::[\w-]+/gi) ?? []).length;
  return count;
}
/** Whether `a` wins over `b` when `a` comes later in the source. */
const outranks = (a: number[], b: number[]) =>
  (a[0]! - b[0]! || a[1]! - b[1]! || a[2]! - b[2]!) >= 0;

/** Properties whose values can paint a colour. */
const PAINTS =
  /color|background|border|outline|shadow|fill|stroke|caret|accent|column-rule|text-decoration|filter/;
/** The words those values may use besides tokens. None of them names a colour. */
const NOT_COLOURS = new Set([
  ...["none", "transparent", "currentcolor", "inherit", "initial", "unset"],
  ...["important", "solid", "dashed", "dotted", "double", "inset"],
  ...["linear-gradient", "radial-gradient", "ellipse", "circle", "at", "to"],
  ...["top", "right", "bottom", "left", "center"],
  // `color-scheme`
  ...["light", "dark"],
]);
/** A value's words, leaving out tokens, `url(#fragment)`, strings and numbers. */
const words = (value: string) =>
  value
    .replace(/var\(--[\w-]+\)|url\(#[^)]*\)|"[^"]*"|'[^']*'|[+-]?[\d.]+[a-z%]*/gi, " ")
    .toLowerCase()
    .match(/[a-z-]+/g) ?? [];
/** The styled declarations that fail `test`, as `selector { property: value }`. */
const offending = (test: (property: string, value: string) => boolean) =>
  styled.flatMap(({ selector, declarations }) =>
    declarations
      .filter(([property, value]) => test(property, value))
      .map(([property, value]) => `${selector} { ${property}: ${value} }`),
  );

// A control is 44 px square with a 20 px icon at its centre (player.tsx).
const ICON = 20;
// On a phone in landscape the safe-area padding grows to the home indicator's
// inset, 21 px on an iPhone.
const INSET = 21;
const length = (value: string) => {
  const max = /^max\(([\d.]+)px, env\([\w-]+\)\)$/.exec(value);
  return max ? Math.max(Number(max[1]), INSET) : parseFloat(value);
};
const SIDES = ["top", "right", "bottom", "left"];
/** The padding of `selector` at each at-rule that changes it, top first. */
function paddings(selector: string) {
  const boxes = new Map<string, number[]>();
  for (const r of rules)
    if (subjects(r.selector).includes(selector))
      for (const [property, value] of r.declarations) {
        if (!property.startsWith("padding")) continue;
        const box = [...(boxes.get(r.at) ?? boxes.get("") ?? [])];
        if (property === "padding") {
          const [top, right = top, bottom = top, left = right] = value
            .split(" ")
            .map(length);
          box.splice(0, 4, top!, right!, bottom!, left!);
        } else {
          const side = SIDES.indexOf(property.slice("padding-".length));
          if (side < 0) throw new Error(`${r.selector} sets ${property}`);
          box[side] = length(value);
        }
        boxes.set(r.at, box);
      }
  return boxes;
}

describe("viewer styles", () => {
  it("take every colour, font and font size from a token", () => {
    const literal =
      /#[0-9a-f]{3,8}\b|\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color|color-mix|light-dark)\(/i;
    expect(
      offending((_, value) => literal.test(value.replace(/url\(#[^)]*\)/g, ""))),
    ).toEqual([]);
    // Named colours, which a list of every name would miss as CSS grows.
    expect(
      offending(
        (property, value) =>
          PAINTS.test(property) && words(value).some((w) => !NOT_COLOURS.has(w)),
      ),
    ).toEqual([]);
    expect(
      offending(
        (property, value) =>
          /^font-(?:size|family)$/.test(property) &&
          !/^(?:var\(--[\w-]+\)|inherit)$/.test(value),
      ),
    ).toEqual([]);
    // The shorthand may add a weight and a line height, as plain numbers.
    expect(
      offending(
        (property, value) =>
          property === "font" &&
          !/^(?:inherit)?$/.test(
            value.replace(/var\(--[\w-]+\)|(?<![\w.-])[\d.]+(?![\w.%])|\//g, "").trim(),
          ),
      ),
    ).toEqual([]);
  });

  it("define every token the rules use, use every token, and hold only tokens in their rules", () => {
    expect(TOKEN_RULES).not.toContain(undefined);
    expect(
      TOKEN_RULES.flatMap((r) => r?.declarations ?? [])
        .map(([name]) => name)
        .filter((name) => !name.startsWith("--")),
    ).toEqual([]);
    // The dark theme only replaces tokens, so both themes have each one. It
    // comes after them, or they would win, and no other rule redefines a token.
    expect(darkRule?.declarations.filter(([name]) => !light.has(name))).toEqual([]);
    expect(rules.indexOf(darkRule!)).toBeGreaterThan(rules.indexOf(tokenRule!));
    expect(
      styled.flatMap((r) =>
        r.declarations
          .filter(([name]) => light.has(name))
          .map(([name]) => `${r.selector} { ${name} }`),
      ),
    ).toEqual([]);
    const used = new Set(
      [...source.matchAll(/var\((--[\w-]+)/g)].map(([, name]) => name!),
    );
    const defined = new Set(
      rules.flatMap((r) => r.declarations.map(([property]) => property)),
    );
    expect([...used].filter((name) => !defined.has(name))).toEqual([]);
    expect([...light.keys()].filter((name) => !used.has(name))).toEqual([]);
  });

  it("let the browser draw its own parts in the page's scheme, and the stage's dark", () => {
    // Without it, a dark page would keep light scrollbars.
    expect(
      rules
        .filter((r) => r.selector === ":root" && !r.at)
        .map((r) => get(r, "color-scheme"))
        .filter(Boolean),
    ).toEqual(["light dark"]);
    expect(get(rule(".video-stage"), "color-scheme")).toBe("dark");
  });

  it("put what sits on the video on the backings its contrast assumes", () => {
    for (const [selector, property, value] of BACKINGS)
      expect(get(rule(selector), property), `${selector} ${property}`).toBe(
        value,
      );
    // No other rule may change them, in any state or at-rule.
    for (const [styles, property, selectors] of SETTERS)
      expect(
        rules
          .filter(
            (r) =>
              subjects(r.selector).some((s) => styles.test(s)) &&
              r.declarations.some(([name]) => property.test(name)),
          )
          .map((r) => r.selector),
        `${styles} ${property}`,
      ).toEqual(selectors);
  });

  // The page's button rules apply on the stage too, so the stage's rules must
  // win the cascade there, in each state, for every colour they set.
  it("give primary and secondary buttons on the stage its colours in every state", () => {
    for (const role of [".primary-button", ".secondary-button"])
      for (const hover of [false, true])
        for (const disabled of [false, true])
          for (const property of ["background", "color", "border-color"]) {
            let winner: { selector: string; weight: number[] } | undefined;
            for (const r of rules) {
              if (r.at) continue;
              const sets = r.declarations.some(
                ([name]) =>
                  name === property || (property === "border-color" && name === "border"),
              );
              if (!sets) continue;
              for (const selector of splitTop(r.selector, /,/)) {
                const parts = splitTop(selector, /[\s>+~]/);
                const subject = parts.at(-1)!;
                const above = parts.slice(0, -1).join(" ");
                if (!subject.includes(role) || (above && above !== ".video-stage")) continue;
                const enabledOnly = subject.includes(":not(:disabled)");
                if (subject.includes(":hover") && !hover) continue;
                if (enabledOnly && disabled) continue;
                if (!enabledOnly && subject.includes(":disabled") && !disabled) continue;
                const weight = specificity(selector);
                if (!winner || outranks(weight, winner.weight)) winner = { selector, weight };
              }
            }
            const state = `${role}${hover ? ":hover" : ""}${disabled ? ":disabled" : ""}`;
            expect(winner?.selector, `${state} ${property}`).toMatch(/^\.video-stage /);
          }
  });

  it("keep toolbar icons on the solid shade, and focus halos clear of edges and tooltips", () => {
    const solid =
      Number(
        /var\(--toolbar-shade\) ([\d.]+)%/.exec(
          get(rule(".player-toolbar"), "background") ?? "",
        )?.[1],
      ) / 100;
    const button = parseFloat(get(rule(".control-button"), "height") ?? "");
    const halo = Number(
      /^0 0 0 ([\d.]+)px /.exec(
        get(rule(".video-stage :focus-visible"), "box-shadow") ?? "",
      )?.[1],
    );
    const toolbars = paddings(".player-toolbar");
    expect(toolbars.size).toBeGreaterThanOrEqual(3);
    for (const [at, [top, right, bottom, left]] of toolbars) {
      const where = at || "the toolbar";
      const iconTop = bottom! + (button + ICON) / 2;
      expect(iconTop, where).toBeLessThanOrEqual(
        solid * (top! + button + bottom!),
      );
      expect(Math.min(right!, bottom!, left!), where).toBeGreaterThanOrEqual(halo);
    }
    for (const [at, [top, right, , left]] of paddings(".stage-top"))
      expect(Math.min(top!, right!, left!), at || "the stage top").toBeGreaterThanOrEqual(
        halo,
      );
    const tooltipGap = Number(
      /^calc\(100% \+ ([\d.]+)px\)$/.exec(
        get(rule(".control-button::after"), "bottom") ?? "",
      )?.[1],
    );
    expect(tooltipGap).toBeGreaterThan(halo);
    // The skip link's margin reaches past its focus ring.
    const ring = rule(":focus-visible");
    const margin = Number(
      /^0 0 0 ([\d.]+)px /.exec(get(rule(".skip-link"), "box-shadow") ?? "")?.[1],
    );
    expect(margin).toBeGreaterThan(
      parseFloat(get(ring, "outline") ?? "") +
        parseFloat(get(ring, "outline-offset") ?? ""),
    );
  });

  it.each(THEMES)("keep AA contrast for text, icons and focus rings in the %s theme", (_, theme) => {
    const list = pairs(theme);
    expect(list.length).toBeGreaterThan(30);
    // The ratio assumes an opaque background.
    expect(list.filter(([, , [, , , a]]) => a !== 1).map(([what]) => what)).toEqual([]);
    const failing = list
      .map(([what, name, background, needs]) => ({
        what,
        ratio: Math.floor(contrast(colour(theme, name), background) * 100) / 100,
        needs,
      }))
      .filter(({ ratio, needs }) => ratio < needs);
    expect(failing).toEqual([]);
  });

  it("keep the stream select's text at 16 px, so iOS Safari does not zoom in", () => {
    const sizing = rules.filter(
      (r) =>
        subjects(r.selector).some((s) => /^select(?![\w-])/.test(s)) &&
        r.declarations.some(([name]) => /^font(?:-size)?$/.test(name)),
    );
    // Plain `select` selectors, so the last in source order wins.
    expect(sizing.map((r) => r.selector)).toEqual(["button, select", "select"]);
    expect(
      parseFloat(resolve(get(rule("select"), "font-size") ?? "")),
    ).toBeGreaterThanOrEqual(16);
  });
});
