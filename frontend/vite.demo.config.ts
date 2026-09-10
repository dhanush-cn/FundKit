// Build config for the hosted, backend-free demo.
//
// Separate from vite.config.ts so `npm run build` keeps producing exactly the
// application bundle — the demo is an extra artefact, never something that can
// leak into the deployed app.
//
// Engineered by Dhanush C N (github.com/dhanush-cn)
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  // Relative asset URLs, so the output works under a project subpath such as
  // a GitHub Pages /fundkit/ prefix without a rebuild.
  base: './',
  build: {
    outDir: 'dist-demo',
    rollupOptions: { input: 'demo.html' },
  },
});
