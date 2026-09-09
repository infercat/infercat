#!/usr/bin/env python3
"""Export native 096 captures for git; retain full-resolution PNGs and MOVs in the source directory."""
import argparse
from pathlib import Path
import struct
import subprocess

ROOT = Path(__file__).resolve().parents[3]
# FFmpeg 9.0.1: Lanczos downscale, per-image 256-color palette, ordered Bayer dithering.
FILTER = ('[0:v]scale=390:-1:flags=lanczos,split[a][b];'
          '[a]palettegen=max_colors=256[p];[b][p]paletteuse=dither=bayer:bayer_scale=3')


def export(source, output):
    source, output = source.resolve(), output.resolve()
    if source == output:
        raise ValueError('Keep originals outside the export directory')
    files = sorted(source.glob('096-*.png'))
    if not files:
        raise ValueError('No native 096 PNGs found')
    output.mkdir(parents=True, exist_ok=True)
    sizes = []
    for original in files:
        assert struct.unpack('>II', original.read_bytes()[16:24]) == (1170, 2532), original.name
        target = output / original.name
        subprocess.run(['ffmpeg', '-y', '-hide_banner', '-loglevel', 'error', '-i', str(original),
                        '-filter_complex', FILTER, '-frames:v', '1', str(target)], check=True)
        png = target.read_bytes()
        assert struct.unpack('>II', png[16:24]) == (390, 844), target.name
        assert png[25] == 3, 'Expected palette-indexed PNG'
        assert len(png) <= 300_000, target.name + ' exceeds 300 KB'
        sizes.append(len(png))
    print(f'PNG exports: {len(files)} passed / 0 failed / 0 skipped; '
          f'390x844, indexed palette; max {max(sizes)} bytes; total {sum(sizes)} bytes')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path, default=Path('/private/tmp/infercat-096-proof'))
    parser.add_argument('--output', type=Path, default=ROOT / 'web/dev/screenshots')
    args = parser.parse_args()
    export(args.source, args.output)
