import hashlib
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import build
import json
import verify


class PackagingTests(unittest.TestCase):
    def test_notice_names_under_build_ancestor(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp); source=root/"build"/"audio.cpp";source.mkdir(parents=True)
            names=["LICENSE","LICENCE","COPYRIGHT.txt","COPYRIGHT.md","MIT-LICENSE.txt","NOTICE","nested/License","vendor/build/LICENSE"]
            for name in names:
                p=source/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text("notice")
            (source/"build").mkdir();(source/"build/LICENSE").write_text("generated")
            build.copy_notices(source,root/"notices")
            self.assertEqual(sorted(str(p.relative_to(root/"notices")) for p in (root/"notices").rglob("*") if p.is_file()),sorted(names))
            empty=root/"empty";empty.mkdir()
            with self.assertRaisesRegex(ValueError,"no audio.cpp notices"):
                build.copy_notices(empty,root/"none")

    def test_release_verifier_rejects_wrong_source_dirty_and_binary(self):
        for fault in [None,"source","dirty","binary","voices"]:
            with self.subTest(fault=fault),tempfile.TemporaryDirectory() as tmp:
                root=Path(tmp)
                for target in ["darwin-arm64","linux-amd64"]:
                    stage=root/target;(stage/"bin").mkdir(parents=True)
                    hashes={}
                    for name in ["infercat-speech","audiocpp_server"]:
                        p=stage/"bin"/name;p.write_bytes(b"binary");hashes['bin/'+name]=build.digest(p)
                    (stage/"voices.txt").write_text("af_maple\n");hashes["voices.txt"]=build.digest(stage/"voices.txt")
                    (stage/"manifest.json").write_text(json.dumps({'source':'wrong' if fault=='source' else 'tag-sha','source_dirty':fault=='dirty','target':target,'files':hashes}))
                    if fault=='voices':(stage/"voices.txt").write_text("tampered")
                    if fault=='binary':(stage/"bin/infercat-speech").write_bytes(b"tampered")
                    archive=root/(target+".tar.gz")
                    with tarfile.open(archive,"w:gz") as tar:tar.add(stage,arcname=target)
                    archive.with_suffix("").with_suffix(".sha256").write_text(build.digest(archive)+'  '+archive.name+'\n')
                if fault:
                    with self.assertRaises(ValueError):verify.verify(root,'tag-sha')
                else:verify.verify(root,'tag-sha')

    def test_download_checks_existing_cache(self):
        with tempfile.TemporaryDirectory() as tmp:
            cache = Path(tmp)
            (cache / "artifact").write_bytes(b"verified")
            pin = {"file": "artifact", "bytes": 8, "sha256": hashlib.sha256(b"verified").hexdigest()}
            self.assertEqual(build.download(cache, pin), cache / "artifact")
            (cache / "artifact").write_bytes(b"tampered")
            with self.assertRaisesRegex(ValueError, "pin mismatch"):
                build.download(cache, pin)

    def test_download_refuses_size_and_hash_before_rename(self):
        for body in [b"long", b"bad"]:
            with self.subTest(body=body), tempfile.TemporaryDirectory() as tmp:
                pin = {"file": "artifact", "bytes": 3, "sha256": hashlib.sha256(b"yes").hexdigest()}
                with patch("urllib.request.urlopen", return_value=io.BytesIO(body)):
                    with self.assertRaises(ValueError):
                        build.download(Path(tmp), pin)
                self.assertEqual(list(Path(tmp).iterdir()), [])

    def test_archive_refuses_traversal_and_devices(self):
        for kind in ["parent", "symlink", "device"]:
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                archive = root / "test.tar"
                with tarfile.open(archive, "w") as tar:
                    entry = tarfile.TarInfo("../escape" if kind == "parent" else "file")
                    if kind == "symlink":
                        entry.type, entry.linkname = tarfile.SYMTYPE, "/tmp/outside"
                    elif kind == "device":
                        entry.type = tarfile.CHRTYPE
                    tar.addfile(entry)
                with self.assertRaises(tarfile.FilterError):
                    build.unpack(archive, root / "out")
                self.assertFalse((root / "escape").exists())

    def test_dependency_paths_are_closed(self):
        fixtures = [
            ("darwin", "/binary:\n\t/opt/homebrew/lib/libbad.dylib (compatibility version 1)"),
            ("linux", "(NEEDED) Shared library: [libbad.so]"),
            ("linux", "(NEEDED) Shared library: [libc.so.6]\n(RUNPATH) Library runpath: [/build/lib]"),
        ]
        for system, output in fixtures:
            with self.subTest(system=system, output=output), patch.object(build, "run", return_value=output):
                with self.assertRaises(ValueError):
                    build.dependencies(Path("binary"), system)

    def test_relative_linux_dependency_allowed(self):
        output = "(NEEDED) Shared library: [libsherpa-onnx-c-api.so]\n(RUNPATH) Library runpath: [$ORIGIN/../sherpa/lib]"
        with patch.object(build, "run", return_value=output):
            self.assertEqual(build.dependencies(Path("binary"), "linux"), ["libsherpa-onnx-c-api.so"])


if __name__ == "__main__":
    unittest.main()
