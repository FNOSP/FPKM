import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// 说明：@douyinfe/semi-ui 的 package.json exports 字段屏蔽了 dist/css/*，
// 因此无法直接 `import '@douyinfe/semi-ui/dist/css/semi.min.css'`。
// 这里用别名 @semi-css 指向实际文件，保证样式可被打包。
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
      '@semi-css': path.resolve(__dirname, 'node_modules/@douyinfe/semi-ui/dist/css/semi.min.css'),
    },
  },
  build: {
    outDir: path.resolve(__dirname, '../internal/web/dist'),
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:7667',
    },
  },
});
