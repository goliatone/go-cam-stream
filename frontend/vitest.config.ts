import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "jsdom",
    restoreMocks: true,
    // styles.test.ts imports the stylesheet's text with `?raw`; every other CSS
    // import stays an empty module.
    css: { include: [/\/src\/styles\.css\?raw$/] },
  },
});
