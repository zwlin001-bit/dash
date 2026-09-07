import { build } from 'esbuild';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import fs from 'node:fs';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const webDir = path.resolve(__dirname, '..');
const outDir = path.join(webDir, '.test-dist');

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
