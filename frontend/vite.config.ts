import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// 开发时把 /api 与 /internal 代理到后端，避免跨域；生产由同源反代处理。
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        // target: 'http://localhost:8080',
        target: 'http://43.138.64.202/',
        changeOrigin: true,
        ws: true,
      },
    },
  },
});
