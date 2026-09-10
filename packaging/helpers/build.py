#!/usr/bin/env python3
"""Build a helper snapshot. Publishing is exclusively the tagged workflow's job."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
PINS = json.loads((Path(__file__).with_name("pins.json")).read_text())


def run(*args, **kwargs):
    return subprocess.check_output([str(a) for a in args], text=True, **kwargs).strip()


def digest(path):
    with path.open("rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


def download(cache, pin):
    dest = cache / pin["file"]
    if not dest.exists():
        url = f'https://github.com/k2-fsa/sherpa-onnx/releases/download/{PINS["sherpa"]}/{pin["file"]}'
        part = dest.with_suffix(".partial")
        try:
            with urllib.request.urlopen(url, timeout=60) as response, part.open("xb") as out:
                total = 0
                while block := response.read(1 << 20):
                    total += len(block)
                    if total > pin["bytes"]:
                        raise ValueError("sherpa archive exceeds pinned size")
                    out.write(block)
            if part.stat().st_size != pin["bytes"] or digest(part) != pin["sha256"]:
                raise ValueError("sherpa archive pin mismatch")
            part.rename(dest)
        finally:
            part.unlink(missing_ok=True)
    if not dest.is_file() or dest.stat().st_size != pin["bytes"] or digest(dest) != pin["sha256"]:
        raise ValueError("cached sherpa archive pin mismatch")
    return dest


def unpack(archive, target):
    with tarfile.open(archive) as tar:
        members = tar.getmembers()
        if len(members) > 10000 or sum(m.size for m in members) > 512 << 20:
            raise ValueError("archive expansion bound")
        tar.extractall(target, members=members, filter="data")


def copy_notices(audio, target):
    copied = 0
    for src in audio.rglob("*"):
        rel, name = src.relative_to(audio), src.name.upper()
        if src.is_file() and ".git" not in rel.parts and rel.parts[0] != "build" and (name.startswith(("LICENSE", "LICENCE", "NOTICE", "COPYING", "COPYRIGHT")) or "-LICENSE" in name):
            dst = target / rel
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dst)
            copied += 1
    if not copied:
        raise ValueError("no audio.cpp notices found")


def dependencies(binary, system):
    if system == "darwin":
        deps = [line.strip().split(" (", 1)[0] for line in run("otool", "-L", binary).splitlines()[1:]]
        for dep in deps:
            if not dep.startswith(("/System/Library/", "/usr/lib/")) and dep != "@rpath/libsherpa-onnx-c-api.dylib":
                raise ValueError(f"non-relocatable dependency: {dep}")
        paths = re.findall(r"cmd LC_RPATH.*?\n\s*path (.*?) \(offset", run("otool", "-l", binary), re.S)
        if any(path != "@loader_path/../sherpa/lib" for path in paths):
            raise ValueError(f"non-relocatable runtime paths: {paths}")
    else:
        raw = run("readelf", "-d", binary)
        deps = re.findall(r"\(NEEDED\).*\[(.*?)\]", raw)
        allowed = {"libsherpa-onnx-c-api.so", "libstdc++.so.6", "libm.so.6", "libgcc_s.so.1", "libc.so.6", "libpthread.so.0", "libdl.so.2", "librt.so.1", "libresolv.so.2", "ld-linux-x86-64.so.2"}
        if set(deps) - allowed:
            raise ValueError(f"unexpected dynamic dependencies: {deps}")
        paths = re.findall(r"\((?:RUNPATH|RPATH)\).*\[(.*?)\]", raw)
        if any(path != "$ORIGIN/../sherpa/lib" for path in paths):
            raise ValueError(f"non-relocatable runtime paths: {paths}")
    return deps


def build(audio, work, version):
    system = platform.system().lower()
    arch = {"aarch64": "arm64", "x86_64": "amd64"}.get(platform.machine(), platform.machine())
    target = f"{system}-{arch}"
    if target not in PINS or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,80}", version):
        raise ValueError("unsupported target or invalid version")
    if run("git", "-C", audio, "rev-parse", "HEAD") != PINS["audio"] or run("git", "-C", audio, "status", "--porcelain"):
        raise ValueError("audio.cpp must be a clean checkout of the pinned commit")
    cache = work / "cache"
    cache.mkdir(parents=True, exist_ok=True)
    archive = download(cache, PINS[target])
    unpack(archive, work / "dependency")
    sherpa = work / "dependency" / PINS[target]["file"].removesuffix(".tar.bz2")
    stage = work / f"infercat-helpers_{version}_{target}"
    stage.mkdir()
    (stage / "bin").mkdir()
    env = dict(os.environ, CGO_ENABLED="1", CGO_CFLAGS=f'-I{sherpa / "include"}')
    origin = "@loader_path" if system == "darwin" else "$ORIGIN"
    env["CGO_LDFLAGS"] = f'-L{sherpa / "lib"} -Wl,-rpath,{origin}/../sherpa/lib'
    if system == "darwin":
        env["MACOSX_DEPLOYMENT_TARGET"] = "14.0"
    subprocess.run(["go", "build", "-trimpath", "-tags", "sherpa", "-ldflags", f"-s -w -X main.version={version}", "-o", str(stage / "bin/infercat-speech"), "./cmd/infercat-speech"], cwd=ROOT, env=env, check=True)
    output = work / "audio-build"
    flags = ["-DCMAKE_BUILD_TYPE=Release", "-DBUILD_SHARED_LIBS=OFF", "-DAUDIOCPP_MODEL_SET=custom", "-DAUDIOCPP_MODELS=fun_asr_nano", "-DAUDIOCPP_BUILD_NATIVE_MODEL_MANAGER=OFF", "-DENGINE_ENABLE_NATIVE_CPU=OFF", "-DGGML_NATIVE=OFF", "-DENGINE_ENABLE_CUDA=OFF", "-DENGINE_ENABLE_VULKAN=OFF", "-DENGINE_ENABLE_OPENMP=OFF", "-DGGML_OPENMP=OFF", "-DGGML_METAL_EMBED_LIBRARY=ON"]
    flags += [f'-DENGINE_ENABLE_METAL={"ON" if system == "darwin" else "OFF"}']
    if system == "darwin":
        flags += ["-DCMAKE_OSX_DEPLOYMENT_TARGET=14.0"]
    else:
        flags += [f"-DGGML_{isa}=OFF" for isa in ["AVX", "AVX2", "FMA", "F16C", "AVX512"]]
    subprocess.run(["cmake", "-S", str(audio), "-B", str(output), *flags], check=True)
    subprocess.run(["cmake", "--build", str(output), "--target", "audiocpp_server", "--parallel", "4"], check=True)
    shutil.copy2(output / "bin/audiocpp_server", stage / "bin/audiocpp_server")
    deps = {p.name: dependencies(p, system) for p in (stage / "bin").iterdir()}
    notices = stage / "licenses"
    notices.mkdir()
    shutil.copy2(ROOT / "LICENSE", notices / "infercat-LICENSE")
    shutil.copy2(Path(__file__).with_name("LICENSE-sherpa"), notices / "sherpa-C-API-LICENSE")
    # Carry the pinned source's own and vendored notices, including nested notices.
    copy_notices(audio, notices / "audio.cpp")
    shutil.copy2(Path(__file__).with_name("README.md"), stage / "README.md")
    shutil.copy2(ROOT / "internal/speech/voices.txt", stage / "voices.txt")
    manifest = {"version": version, "target": target, "source": run("git", "-C", ROOT, "rev-parse", "HEAD"), "source_dirty": bool(run("git", "-C", ROOT, "status", "--porcelain", "--untracked-files=no")), "audio_source": PINS["audio"], "sherpa": PINS[target], "dependencies": deps, "go": run("go", "version"), "cmake": run("cmake", "--version").splitlines()[0], "files": {str(p.relative_to(stage)): digest(p) for p in [*(stage / "bin").iterdir(), stage / "voices.txt"]}}
    (stage / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    tarpath = work / (stage.name + ".tar.gz")
    with tarfile.open(tarpath, "w:gz") as tar:
        tar.add(stage, arcname=stage.name)
    # Relocate the artifact and the independently verified upstream dependency.
    with tempfile.TemporaryDirectory(prefix="helpers-relocate-") as tmp:
        relocated = Path(tmp)
        unpack(tarpath, relocated)
        install = relocated / stage.name
        shutil.copytree(sherpa / "lib", install / "sherpa/lib", symlinks=True)
        clean = {k: v for k, v in os.environ.items() if not k.startswith(("DYLD_", "LD_", "CGO_"))}
        print(run(install / "bin/infercat-speech", "--version", cwd=relocated, env=clean))
        print(run(install / "bin/audiocpp_server", "--version", cwd=relocated, env=clean))
    (work / (stage.name + ".sha256")).write_text(f"{digest(tarpath)}  {tarpath.name}\n")
    print(tarpath)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--audio-source", type=Path, required=True)
    parser.add_argument("--work", type=Path, required=True)
    parser.add_argument("--version", default="snapshot")
    args = parser.parse_args()
    build(args.audio_source.resolve(), args.work.resolve(), args.version)
