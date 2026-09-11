// Render the actual ANSI TUI snapshot into a portable README image.
// Run via `make preview`. This script is a docs utility, not part of Skillverk.
import fs from 'node:fs';
import { createHash } from 'node:crypto';

const source = fs.readFileSync(new URL('../docs/preview.ansi', import.meta.url), 'utf8');
const columns = 112, cell = 9, rowHeight = 20, inset = 24, top = 54;
const lines = source.split('\n');
const width = columns * cell + inset * 2;
const height = lines.length * rowHeight + top + 20;
const escape = s => s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;');
const palette = ['#10181e','#ef8b8b','#8ce3c6','#f4c47c','#82aaff','#c792ea','#89ddff','#e3e8ed'];
const indexed = n => {
  if (n < 16) return palette[n % 8];
  if (n > 231) { const c = 8 + (n - 232) * 10; return `rgb(${c},${c},${c})`; }
  const a = [0,95,135,175,215,255]; n -= 16;
  return `rgb(${a[Math.floor(n/36)]},${a[Math.floor(n/6)%6]},${a[n%6]})`;
};
let foreground = '#e3e8ed', background = '#10181e', bold = false, underline = false;
const reset = () => { foreground = '#e3e8ed'; background = '#10181e'; bold = false; underline = false; };
const elements = [];
for (const [row, line] of lines.entries()) {
  let x = inset;
  for (const token of line.split(/(\x1b\[[0-9;:]*m|\x1b\]8;[^\x07\x1b]*(?:\x07|\x1b\\))/g)) {
    if (token.startsWith('\x1b]8;')) continue;
    if (token.startsWith('\x1b[')) {
      const codes = token.slice(2,-1).split(';').map(Number);
      for (let i=0; i<codes.length; i++) {
        const c = codes[i];
        if (c===0) reset();
        else if(c===1) bold=true;
        else if(c===22) bold=false;
        else if(c===4) underline=true;
        else if(c===24) underline=false;
        else if(c===39) foreground='#e3e8ed';
        else if(c===49) background='#10181e';
        else if(c===38 || c===48) {
          let color;
          if(codes[i+1]===2) { color=`rgb(${codes[i+2]},${codes[i+3]},${codes[i+4]})`; i+=4; }
          else if(codes[i+1]===5) { color=indexed(codes[i+2]); i+=2; }
          if(color) { if(c===38) foreground=color; else background=color; }
        } else if(c>=30 && c<=37) foreground=palette[c-30];
        else if(c>=40 && c<=47) background=palette[c-40];
      }
      continue;
    }
    const span = [...token].length * cell;
    if (background !== '#10181e' && span) elements.push(`<rect x="${x}" y="${top+row*rowHeight-15}" width="${span}" height="${rowHeight}" fill="${background}"/>`);
    if (token.trim()) {
      const text = `<text x="${x}" y="${top+row*rowHeight}" fill="${foreground}" font-weight="${bold?600:400}"${underline ? ' text-decoration="underline"' : ''} xml:space="preserve">${escape(token)}</text>`;
      elements.push(text);
    }
    x += span;
  }
}
const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">
<title>Skillverk showing grill-with-docs from mattpocock/skills</title>
<desc>Actual repository view with selected skills first, the remaining library underneath, and immediate keyboard controls. Both agents share one repository setting.</desc>
<rect width="${width}" height="${height}" rx="16" fill="#10181e"/>
<path d="M0 38H${width}" stroke="#30414b"/>
<circle cx="23" cy="20" r="5" fill="#ef8b8b"/><circle cx="41" cy="20" r="5" fill="#f4c47c"/><circle cx="59" cy="20" r="5" fill="#8ce3c6"/>
<text x="${width/2}" y="25" text-anchor="middle" font-family="sans-serif" font-size="12" fill="#82929e">skillverk</text>
<g font-family="DejaVu Sans Mono, monospace" font-size="15">${elements.join('\n')}</g>
</svg>\n`;
// A new filename prevents README viewers from reusing an older image.
const readmePath = new URL('../README.md', import.meta.url);
const readme = fs.readFileSync(readmePath, 'utf8');
const previous = readme.match(/docs\/skill-picker(?:-[a-f0-9]{10})?\.svg/)?.[0];
if (!previous) throw new Error('README screenshot link not found');
const digest = createHash('sha256').update(svg).digest('hex').slice(0, 10);
const output = `docs/skill-picker-${digest}.svg`;
fs.writeFileSync(new URL(`../${output}`, import.meta.url), svg);
fs.writeFileSync(readmePath, readme.replace(previous, output));
if (previous !== output) fs.rmSync(new URL(`../${previous}`, import.meta.url), { force: true });
console.log(`Rendered ${output} and updated the README link.`);
