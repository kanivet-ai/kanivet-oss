/*
 * Regenerates every Kanivet brand asset from the monogram geometry in
 * src/components/icons/kanivetMark.json, so the dock icon, tray icon, and in-app mark never
 * drift apart.
 *
 *   npm run build:icons          (runs: electron scripts/build-icons.cjs)
 *
 * Outputs
 *   assets/icon.svg                   canonical full-bleed tile, 1024×1024
 *   assets/icon.png                   Linux / AppImage, full-bleed 1024×1024
 *   assets/icon.ico                   Windows, full-bleed 16–256
 *   assets/icon.icns                  macOS, Apple's 824-in-1024 composition with tile shadow (needs iconutil)
 *   assets/icon-layers/*.svg          unmasked layers for Icon Composer (background, stem, arm, leg)
 *   public/kanivet-icon.png           tray fallback on non-mac, full-bleed 512×512 (+ copy in resources/)
 *   public/trayIconTemplate[@2x].png  macOS menu-bar template: black glyph on transparent (+ copies)
 *
 * Rasterising goes through Electron's own Chromium so the output matches what the app renders.
 */
const { app, BrowserWindow } = require('electron');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { execFileSync } = require('child_process');
const { tileSvg, glyphSvg, layerSvgs } = require('./kanivet-mark-svg.cjs');

const ROOT = path.resolve(__dirname, '..');

const write = (rel, data) => {
  const abs = path.join(ROOT, rel);
  fs.mkdirSync(path.dirname(abs), { recursive: true });
  fs.writeFileSync(abs, data);
  console.log(`  wrote ${rel} (${data.length} bytes)`);
};

/** PNG-compressed ICO container. */
const buildIco = (entries) => {
  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(entries.length, 4);
  const dir = Buffer.alloc(16 * entries.length);
  let offset = header.length + dir.length;
  entries.forEach(({ size, png }, i) => {
    const o = i * 16;
    dir[o] = size >= 256 ? 0 : size;
    dir[o + 1] = size >= 256 ? 0 : size;
    dir.writeUInt16LE(1, o + 4); // colour planes
    dir.writeUInt16LE(32, o + 6); // bits per pixel
    dir.writeUInt32LE(png.length, o + 8);
    dir.writeUInt32LE(offset, o + 12);
    offset += png.length;
  });
  return Buffer.concat([header, dir, ...entries.map((e) => e.png)]);
};

let dpr = 1;
const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'kanivet-icons-'));

/** Render an SVG string to a PNG buffer of exactly size×size pixels. */
const rasterise = async (svg, size) => {
  const css = size / dpr;
  const win = new BrowserWindow({
    show: false,
    width: Math.max(1, Math.round(css)),
    height: Math.max(1, Math.round(css)),
    transparent: true,
    frame: false,
    backgroundColor: '#00000000',
    webPreferences: { offscreen: true, sandbox: false },
  });
  const html =
    '<!doctype html><html><head><meta charset="utf-8"><style>html,body{margin:0;background:transparent;overflow:hidden}' +
    `svg{display:block;width:${css}px;height:${css}px}</style></head><body>${svg}</body></html>`;
  const page = path.join(scratch, `page-${size}-${Date.now()}.html`);
  fs.writeFileSync(page, html);
  await win.loadFile(page);
  await new Promise((r) => setTimeout(r, 60));
  let img = await win.webContents.capturePage();
  win.destroy();
  fs.rmSync(page, { force: true });
  const got = img.getSize();
  if (got.width !== size || got.height !== size) {
    img = img.resize({ width: size, height: size, quality: 'best' });
  }
  return img.toPNG();
};

const detectDpr = async () => {
  const win = new BrowserWindow({
    show: false,
    width: 100,
    height: 100,
    webPreferences: { offscreen: true, sandbox: false },
  });
  const page = path.join(scratch, 'probe.html');
  fs.writeFileSync(page, '<body style="margin:0;background:#000">');
  await win.loadFile(page);
  const img = await win.webContents.capturePage();
  win.destroy();
  dpr = img.getSize().width / 100 || 1;
};

const main = async () => {
  await detectDpr();
  console.log(`Rendering with device scale factor ${dpr}`);

  // Canonical vector.
  write('assets/icon.svg', tileSvg(1024));

  // Icon Composer layers.
  for (const [name, svg] of Object.entries(layerSvgs()))
    write(`assets/icon-layers/${name}`, svg);

  // Linux.
  write('assets/icon.png', await rasterise(tileSvg(1024), 1024));

  // Windows.
  const icoSizes = [16, 24, 32, 48, 64, 128, 256];
  const icoEntries = [];
  for (const size of icoSizes)
    icoEntries.push({ size, png: await rasterise(tileSvg(size), size) });
  write('assets/icon.ico', buildIco(icoEntries));

  // Tray fallback (non-mac), resized to 16px at runtime.
  const trayFallback = await rasterise(tileSvg(512), 512);
  write('public/kanivet-icon.png', trayFallback);
  write('resources/kanivet-icon.png', trayFallback);

  // macOS menu-bar template: black glyph, system recolours it.
  const tray1x = await rasterise(glyphSvg(36, '#000'), 36);
  const tray2x = await rasterise(glyphSvg(72, '#000'), 72);
  write('public/trayIconTemplate.png', tray1x);
  write('public/trayIconTemplate@2x.png', tray2x);
  write('resources/trayIconTemplate.png', tray1x);
  write('resources/trayIconTemplate@2x.png', tray2x);

  // macOS app icon: Apple's template puts an 824px tile with a soft shadow on a 1024px canvas.
  if (process.platform === 'darwin') {
    const iconset = fs.mkdtempSync(path.join(os.tmpdir(), 'kanivet-iconset-'));
    const setDir = path.join(iconset, 'icon.iconset');
    fs.mkdirSync(setDir);
    for (const base of [16, 32, 128, 256, 512]) {
      for (const scale of [1, 2]) {
        const px = base * scale;
        const inset = Math.round((px * 100) / 1024);
        const png = await rasterise(tileSvg(px, inset), px);
        const name = `icon_${base}x${base}${scale === 2 ? '@2x' : ''}.png`;
        fs.writeFileSync(path.join(setDir, name), png);
      }
    }
    const out = path.join(ROOT, 'assets/icon.icns');
    execFileSync('iconutil', ['-c', 'icns', setDir, '-o', out]);
    console.log(`  wrote assets/icon.icns (${fs.statSync(out).size} bytes)`);
    fs.rmSync(iconset, { recursive: true, force: true });
  } else {
    console.log('  skipped assets/icon.icns (iconutil is macOS-only)');
  }
};

app.disableHardwareAcceleration();
// Windows are created and destroyed one at a time; don't let Electron quit in between.
app.on('window-all-closed', () => {});
app
  .whenReady()
  .then(main)
  .then(
    () => {
      fs.rmSync(scratch, { recursive: true, force: true });
      app.exit(0);
    },
    (err) => {
      console.error(err);
      fs.rmSync(scratch, { recursive: true, force: true });
      app.exit(1);
    },
  );
