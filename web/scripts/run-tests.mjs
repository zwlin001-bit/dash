import { build } from 'esbuild';
import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import fs from 'node:fs';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const webDir = path.resolve(__dirname, '..');
const outDir = path.join(webDir, '.test-dist');

// 机制 ❷: 契约类型静态检查 (tsc --noEmit)
// 任何前端 TS 类型声明与调用不匹配直接阻断测试
const tsc = spawnSync('npx', ['tsc', '--noEmit'], {
  stdio: 'inherit',
  cwd: webDir,
});
if (tsc.status !== 0) {
  console.error('❌ TypeScript 契约与类型检查失败，中止测试。');
  process.exit(tsc.status ?? 1);
}

fs.mkdirSync(outDir, { recursive: true });
const bundlePath = path.join(outDir, 'test-bundle.cjs');

try {
  await build({
    entryPoints: [path.join(webDir, 'test/robustness.test.tsx')],
    outfile: bundlePath,
    bundle: true,
    format: 'cjs',
    platform: 'node',
    logOverride: {
      'empty-import-meta': 'silent',
    },
    loader: {
      '.css': 'empty',
    },
    external: ['react', 'react-dom', 'react-dom/server', 'node:*'],
  });

  const child = spawn(process.execPath, ['--test', bundlePath], {
    stdio: 'inherit',
    cwd: webDir,
  });

  child.on('close', (code) => {
    // 清理打包产物
    try {
      fs.rmSync(outDir, { recursive: true, force: true });
    } catch {
      // ignore
    }
    process.exit(code ?? 0);
  });
} catch (err) {
  console.error(err);
  try {
    fs.rmSync(outDir, { recursive: true, force: true });
  } catch {
    // ignore
  }
  process.exit(1);
}
