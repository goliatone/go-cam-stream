import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

// Standalone guest artifact for Camera Sharing. The helper serves
// dist-share/index.html at /s/{share_id} and maps /share-assets/{name} to
// dist-share/assets/{name}; asset names must be flat and match its allowlist.
// The guest CSP allows only same-origin scripts, styles and images, so nothing
// is inlined and no data: URIs are emitted.
export default defineConfig({
  root: fileURLToPath(new URL("./share/", import.meta.url)),
  base: "/share-assets/",
  publicDir: false,
  build: {
    outDir: process.env.CAMSTREAM_SHARE_OUT_DIR ?? fileURLToPath(new URL("./dist-share/", import.meta.url)),
    emptyOutDir: true,
    target: "es2022",
    assetsDir: "assets",
    assetsInlineLimit: 0,
    cssCodeSplit: false,
    modulePreload: false,
    sourcemap: false,
    rolldownOptions: {
      output: {
        entryFileNames: "assets/guest-[hash].js",
        chunkFileNames: "assets/guest-[hash].js",
        assetFileNames: "assets/guest-[hash][extname]",
      },
    },
  },
  experimental: {
    // Served as /share-assets/{name}, without the directory.
    renderBuiltUrl: (filename) => `/share-assets/${filename.replace(/^assets\//, "")}`,
  },
});
