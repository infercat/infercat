# Small deterministic documents for extraction and real-host verification; no downloaded content.
from pathlib import Path
from zipfile import ZipFile, ZIP_DEFLATED
from io import BytesIO
from PIL import Image, ImageDraw
from pypdf import PdfReader, PdfWriter
root = Path(__file__).parent

def pdf(objects):
    out = bytearray(b'%PDF-1.4\n'); offsets = [0]
    for i, obj in enumerate(objects, 1):
        offsets.append(len(out)); out.extend(f'{i} 0 obj\n'.encode() + obj + b'\nendobj\n')
    start = len(out)
    out.extend(f'xref\n0 {len(offsets)}\n0000000000 65535 f \n'.encode())
    for offset in offsets[1:]: out.extend(f'{offset:010d} 00000 n \n'.encode())
    out.extend(f'trailer\n<< /Size {len(offsets)} /Root 1 0 R >>\nstartxref\n{start}\n%%EOF\n'.encode())
    return bytes(out)

def stream(content):
    return f'<< /Length {len(content)} >>\nstream\n'.encode() + content + b'\nendstream'

objects = [b'<< /Type /Catalog /Pages 2 0 R >>', b'<< /Type /Pages /Kids [3 0 R 5 0 R] /Count 2 >>',
 b'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 7 0 R >> >> /Contents 4 0 R >>',
 stream(b'BT /F1 18 Tf 50 700 Td (Rate limits for invited keys.) Tj 0 -32 Td (The burst multiplier is two.) Tj ET'),
 b'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 7 0 R >> >> /Contents 6 0 R >>',
 stream(b'BT /F1 18 Tf 50 700 Td (A key may spend twice its limit.) Tj 0 -32 Td (The source file implements that rule.) Tj ET'),
 b'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>']
(root/'rate-limits.pdf').write_bytes(pdf(objects))
writer=PdfWriter(); writer.append(PdfReader(BytesIO(pdf(objects)))); writer.encrypt('fixture-password')
with (root/'encrypted.pdf').open('wb') as f: writer.write(f)
image=Image.new('RGB',(600,180),'white');ImageDraw.Draw(image).text((24,60),'Scanned page: the burst multiplier is two.', fill='black',font_size=24)
image.save(root/'scan.pdf','PDF',resolution=72)
with ZipFile(root/'rate-limits.docx','w',ZIP_DEFLATED) as z:
    z.writestr('[Content_Types].xml','<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>')
    z.writestr('_rels/.rels','<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>')
    z.writestr('word/document.xml','<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>The Word document says the burst multiplier is two.</w:t></w:r></w:p></w:body></w:document>')
(root/'limiter.ts').write_text('export const BURST_MULTIPLIER = 2;\nexport function canSpend(tokens: number, limit: number) {\n  return tokens <= limit * BURST_MULTIPLIER;\n}\n')
(root/'notes.txt').write_bytes('The burst multiplier is two.  \r\n  Preserve this indentation.\r\n'.encode())
(root/'binary.txt').write_bytes(b'Binary masquerading as text\x00\x01\x02')
(root/'non-utf8.txt').write_bytes(b'Not UTF-8 text: \xff\xfe')
print('Created 8 owned extraction fixtures')
