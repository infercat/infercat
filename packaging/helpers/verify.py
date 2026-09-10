"""Verify downloaded helper artifacts against the release's resolved source commit."""
import json
from pathlib import Path
import sys
import tempfile

from build import digest, unpack


def verify(directory, source):
    targets = set()
    for archive in sorted(directory.glob("*.tar.gz")):
        checksum = archive.with_suffix("").with_suffix(".sha256").read_text().split()
        if checksum != [digest(archive), archive.name]:
            raise ValueError("archive checksum mismatch")
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            unpack(archive, root)
            manifests = list(root.glob("*/manifest.json"))
            if len(manifests) != 1:
                raise ValueError("expected one manifest")
            manifest = json.loads(manifests[0].read_text())
            if manifest["source_dirty"] is not False or manifest["source"] != source or manifest["target"] in targets:
                raise ValueError("dirty, wrong-source or duplicate artifact")
            targets.add(manifest["target"])
            if set(manifest["files"]) != {"bin/infercat-speech", "bin/audiocpp_server", "voices.txt"}:
                raise ValueError("unexpected file inventory")
            for name, sha in manifest["files"].items():
                binary = manifests[0].parent / name
                if binary.is_symlink() or digest(binary) != sha:
                    raise ValueError("manifest file hash mismatch")
    if targets != {"darwin-arm64", "linux-amd64"}:
        raise ValueError("both platform snapshots required")


if __name__ == "__main__":
    verify(Path(sys.argv[1]), sys.argv[2])
