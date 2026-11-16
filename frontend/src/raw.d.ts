/** Vite imports a file's text with the `?raw` suffix. */
declare module "*?raw" {
  const text: string;
  export default text;
}

declare module "*.svg?no-inline" { const url: string; export default url; }
