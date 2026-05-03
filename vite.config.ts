import { defineConfig } from 'vite';

export default defineConfig({
  root: 'web',
  build: {
    outDir: 'dist',
    emptyOutDir: true
  },
  server: {
    proxy: {
      '/api': 'http://localhost:3000',
      '/download': 'http://localhost:3000'
    }
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts']
  }
});
