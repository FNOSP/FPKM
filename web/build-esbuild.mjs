/**
 * 低内存前端构建（esbuild 替代 vite build）
 *
 * 背景：本机物理内存 8GB 且没有分页文件，commit 空间经常只剩几百 MB，
 * 而 vite build（Rollup + V8）需要 >320MB 的 JS 堆、进程总 commit 更高，
 * 会直接以 "memory allocation of xxx bytes failed" / "JavaScript heap out of
 * memory" 失败。esbuild 是 Go 程序，内存占用低得多，可以在同样环境下完成构建。
 *
 * 产物形态与 vite build 保持一致：
 *   internal/web/dist/index.html
 *   internal/web/dist/assets/index-<hash>.js
 *   internal/web/dist/assets/index-<hash>.css
 *   internal/web/dist/<public 资源>
 *
 * 用法：node build-esbuild.mjs
 */
import { build } from 'esbuild';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const outDir = path.resolve(__dirname, '../internal/web/dist');
const publicDir = path.resolve(__dirname, 'public');
const tmpDir = path.resolve(__dirname, '.esbuild-tmp');

fs.rmSync(tmpDir, { recursive: true, force: true });
fs.mkdirSync(tmpDir, { recursive: true });

await build({
  entryPoints: [path.resolve(__dirname, 'src/main.tsx')],
  outdir: tmpDir,
  bundle: true,
  minify: true,
  format: 'esm',
  target: 'es2020',
  jsx: 'automatic',
  // 与 vite 保持一致：直接输出 UTF-8，中文文案不转义成 \uXXXX
  charset: 'utf8',
  legalComments: 'none',
  define: { 'process.env.NODE_ENV': '"production"' },
  alias: {
    '@': path.resolve(__dirname, 'src'),
    '@semi-css': path.resolve(
      __dirname,
      'node_modules/@douyinfe/semi-ui/dist/css/semi.min.css',
    ),
  },
  loader: { '.png': 'file', '.jpg': 'file', '.jpeg': 'file', '.svg': 'file', '.webp': 'file' },
  // public/ 下的资源是按 "/xxx.png" 绝对路径引用的（vite 的约定），
  // esbuild 默认会当成模块去解析，这里统一标记为外部依赖，原样保留。
  plugins: [
    {
      name: 'public-absolute-external',
      setup(b) {
        b.onResolve({ filter: /^\// }, (args) => ({ path: args.path, external: true }));
      },
    },
  ],
  entryNames: 'index',
  assetNames: 'asset-[name]-[hash]',
  logLevel: 'info',
});

const shortHash = (buf) => crypto.createHash('md5').update(buf).digest('hex').slice(0, 8);

const js = fs.readFileSync(path.join(tmpDir, 'index.js'));
const cssName = fs.existsSync(path.join(tmpDir, 'index.css')) ? 'index.css' : null;
const css = cssName ? fs.readFileSync(path.join(tmpDir, cssName)) : null;

fs.rmSync(outDir, { recursive: true, force: true });
fs.mkdirSync(path.join(outDir, 'assets'), { recursive: true });

const jsName = `index-${shortHash(js)}.js`;
fs.writeFileSync(path.join(outDir, 'assets', jsName), js);
let cssTag = '';
if (css) {
  const n = `index-${shortHash(css)}.css`;
  fs.writeFileSync(path.join(outDir, 'assets', n), css);
  cssTag = `\n    <link rel="stylesheet" crossorigin href="/assets/${n}">`;
}

// 其它被引用的静态资源（若有）一并拷过去
for (const f of fs.readdirSync(tmpDir)) {
  if (f === 'index.js' || f === 'index.css') continue;
  const src = path.join(tmpDir, f);
  if (fs.statSync(src).isFile()) {
    fs.copyFileSync(src, path.join(outDir, 'assets', f));
  }
}

// public/ 下的静态资源按 vite 的约定原样放到 dist 根目录
for (const f of fs.readdirSync(publicDir)) {
  const src = path.join(publicDir, f);
  const dst = path.join(outDir, f);
  if (fs.statSync(src).isDirectory()) {
    fs.cpSync(src, dst, { recursive: true });
  } else {
    fs.copyFileSync(src, dst);
  }
}

const html = fs
  .readFileSync(path.resolve(__dirname, 'index.html'), 'utf8')
  .replace(
    /\s*<script type="module"[^>]*><\/script>/,
    `\n    <script type="module" crossorigin src="/assets/${jsName}"></script>${cssTag}`,
  );
fs.writeFileSync(path.join(outDir, 'index.html'), html);

fs.rmSync(tmpDir, { recursive: true, force: true });
console.log(`构建完成: dist/assets/${jsName}${css ? ' + index css' : ''}`);