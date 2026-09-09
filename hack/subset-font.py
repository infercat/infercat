#!/usr/bin/env python3
"""Rebuild: python3 hack/subset-font.py /path/to/NotoSansSC[wght].ttf
Uses the existing fontTools[woff] build tool (4.60.2) and the same Google Fonts Noto Sans SC/OFL.
Check only: python3 hack/subset-font.py --check. Add --console for console/copy.ts.
The web and console unit suites also read their shipped font cmap.
"""
import argparse
import ast
import re
from pathlib import Path
from fontTools import subset
from fontTools.ttLib import TTFont

root = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('source', nargs='?')
parser.add_argument('--check', action='store_true')
parser.add_argument('--console', action='store_true', help='subset/check console copy instead of web tables')
args = parser.parse_args()
values = []
if args.console:
    code = (root / 'console/copy.ts').read_text()
    values.extend(ast.literal_eval(m) for m in re.findall(r"""("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')""", code))
else:
    for language in ('en', 'zh'):
        code = (root / f'web/src/i18n/{language}.ts').read_text()
        values.extend(ast.literal_eval(m) for m in re.findall(r"\b\w+:\s*(\"(?:\\.|[^\"\\])*\"|'(?:\\.|[^'\\])*')", code))
text = ''.join(values)
output = root / ('console/public/fonts/noto-sans-sc.woff2' if args.console else 'web/public/fonts/noto-sans-sc.woff2')
if not args.check:
    if not args.source:
        parser.error('source TTF required to rebuild')
    options = subset.Options()
    options.flavor = 'woff2'
    font = subset.load_font(args.source, options)
    worker = subset.Subsetter(options=options)
    worker.populate(text=text)
    worker.subset(font)
    subset.save_font(font, str(output), options)
cjk = {ord(c) for c in text if '\u3400' <= c <= '\u9fff'}
missing = cjk - set(TTFont(output).getBestCmap())
if missing:
    raise SystemExit(f'font: missing {len(missing)} CJK glyphs: {sorted(missing)}')
print(f'font: {len(cjk)} CJK glyphs present, 0 missing; {output.stat().st_size} bytes')
