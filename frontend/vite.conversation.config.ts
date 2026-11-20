import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

export default defineConfig({
  root: fileURLToPath(new URL("./conversation/", import.meta.url)),
  base: "/conversation-assets/",
  publicDir: false,
  build: {
    outDir: process.env.CAMSTREAM_CONVERSATION_OUT_DIR ?? fileURLToPath(new URL("./dist-conversation/", import.meta.url)),
    target: "es2022",
    emptyOutDir: true,
    modulePreload: false,
    assetsInlineLimit: 0,
    sourcemap: false,
    rolldownOptions: { output: { entryFileNames: "app-[hash].js", assetFileNames: "app-[hash][extname]" } },
  },
});
