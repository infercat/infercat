import qrcode from 'qrcode-generator';
// The CLI uses Medium EC and includes four quiet modules on every edge.
qrcode.stringToBytes = s => [...new TextEncoder().encode(s)];
export function inviteQR(content: string): string {
 const qr = qrcode(0, 'M'); qr.addData(content, 'Byte'); qr.make();
 const size = qr.getModuleCount() + 8;
 let cells = '';
 for (let y = 0; y < size - 8; y++) for (let x = 0; x < size - 8; x++) {
  if (qr.isDark(y, x)) cells += `<rect x="${x + 4}" y="${y + 4}" width="1" height="1"/>`;
 }
 return `<svg class="qr" viewBox="0 0 ${size} ${size}" shape-rendering="crispEdges" aria-hidden="true"><rect width="${size}" height="${size}" fill="white"/><g fill="black">${cells}</g></svg>`;
}
