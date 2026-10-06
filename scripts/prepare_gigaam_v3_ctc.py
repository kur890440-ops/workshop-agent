"""DEV ONLY. Run: python scripts/prepare_gigaam_v3_ctc.py
Requires Python >=3.11 and git. Downloads/build tools stay under .tmp.
"""
import argparse
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import subprocess
import sys
import venv

ROOT = Path(__file__).resolve().parents[1]
REVISION = "7447938d791c4f3e643386ee22c33777004293a5"
SOURCE = "https://github.com/salute-developers/GigaAM.git"


def run(*args):
    subprocess.run([str(x) for x in args], cwd=ROOT, check=True)


def sha(path):
    with path.open("rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


def main():
    if sys.flags.optimize:
        raise RuntimeError("Run without -O: export validation assertions must remain enabled")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--worker", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--reuse-export", action="store_true", help="Validate/package an existing export")
    args = parser.parse_args()
    source = ROOT / ".tmp/GigaAM"
    if not args.worker:
        if not source.exists():
            run("git", "clone", SOURCE, source)
            run("git", "-C", source, "checkout", "--detach", REVISION)
        actual = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
        if actual != REVISION:
            raise RuntimeError(f"Expected official checkout {REVISION}; got {actual}. Use a clean .tmp/GigaAM checkout.")
        env = ROOT / ".tmp/day26-venv"
        python = env / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
        if not python.exists():
            venv.create(env, with_pip=True)
        run(python, "-m", "pip", "install", "-r", ROOT / "scripts/gigaam-build-requirements.txt")
        run(python, "-m", "pip", "install", "--no-deps", "--no-build-isolation", "-e", source)
        run(python, __file__, "--worker", *(["--reuse-export"] if args.reuse_export else []))
        return

    import gigaam
    import torch
    from speech_prepare import package

    actual = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
    if actual != REVISION or Path(gigaam.__file__).resolve().parent != source / "gigaam":
        raise RuntimeError("Official source revision/import mismatch")
    if subprocess.check_output(["git", "-C", str(source), "status", "--porcelain", "--untracked-files=no"], text=True).strip():
        raise RuntimeError("Official tracked source was modified")
    torch.set_num_threads(4)
    torch.manual_seed(26)
    export = ROOT / ".tmp/gigaam-export"
    export.mkdir(parents=True, exist_ok=True)
    checkpoint = ROOT / ".tmp/gigaam-checkpoints/v3_ctc.ckpt"
    if not args.reuse_export:
        print("Loading official v3_ctc on CPU, FP32, flash attention disabled", flush=True)
        model = gigaam.load_model("v3_ctc", device="cpu", fp16_encoder=False,
                                  use_flash=False, download_root=str(checkpoint.parent))
        if not model.decoding.tokenizer.charwise or model.decoding.blank_id != 33:
            raise RuntimeError("Unsupported tokenizer/blank ID")
        print("Export path A: model.to_onnx(..., dtype=torch.float32)", flush=True)
        model.to_onnx(str(export), dtype=torch.float32)
        del model
    dest = ROOT / "assets/gigaam-v3-ctc"
    package(export, dest)
    metadata = dict(bundle_version=1, family="GigaAM", model="v3_ctc", source=SOURCE,
                    source_revision=REVISION, export_path="model.to_onnx(dtype=torch.float32)",
                    checkpoint_source=f"{gigaam._URL_DIR}/v3_ctc.ckpt",
                    checkpoint_sha256=sha(checkpoint), dtype="float32", sample_rate=16000,
                    python=sys.version, dependencies={d.metadata['Name']: d.version for d in importlib.metadata.distributions()})
    (dest / "metadata.json").write_text(json.dumps(metadata, indent=2), encoding="utf-8")
    hashes = {p.name: sha(p) for p in sorted(dest.iterdir()) if p.is_file() and p.name != "manifest.json"}
    (dest / "manifest.json").write_text(json.dumps(hashes, indent=2), encoding="utf-8")
    for name, digest in hashes.items():
        if sha(dest / name) != digest:
            raise RuntimeError(f"Hash validation failed: {name}")
    print(json.dumps(dict(bundle=str(dest), model_bytes=(dest / 'model.onnx').stat().st_size,
                          model_sha256=hashes['model.onnx'], validation="PASS"), indent=2), flush=True)


if __name__ == "__main__":
    main()
