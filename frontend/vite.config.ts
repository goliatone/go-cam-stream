import { readFileSync } from "node:fs";
import { Agent } from "node:https";
import { defineConfig } from "vite";

// Development only. Production uses the same-origin Serve ingress. Never inject
// identity headers or disable TLS verification. An optional CA trusts the local
// controlled-media test ingress without altering the browser or system trust.
const origin = process.env.CAMSTREAM_API_ORIGIN;
if (
  origin &&
  (new URL(origin).protocol !== "https:" || new URL(origin).origin !== origin)
)
  throw new Error("CAMSTREAM_API_ORIGIN must be an HTTPS origin");
const ca = process.env.CAMSTREAM_DEV_CA;
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    ...(origin
      ? {
          proxy: {
            "/api": {
              target: origin,
              changeOrigin: true,
              secure: true,
              ...(ca ? { agent: new Agent({ ca: readFileSync(ca) }) } : {}),
              configure(proxy) {
                proxy.on("proxyReq", (outgoing, incoming) => {
                  // The same-origin local browser request becomes a request to Serve.
                  if (
                    incoming.headers.origin ===
                      `http://${incoming.headers.host}` &&
                    /^(127\.0\.0\.1|localhost):[0-9]+$/.test(
                      incoming.headers.host ?? "",
                    )
                  )
                    outgoing.setHeader("Origin", origin);
                });
              },
            },
          },
        }
      : {}),
  },
  preview: { host: "127.0.0.1" },
  // Native dynamic imports fetch optional dialogs only when requested. Small
  // shared styles load with the viewer. The manifest supports payload accounting.
  build: { target: "es2022", modulePreload: false, manifest: true },
});
