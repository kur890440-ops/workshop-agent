"""DEV ONLY: reference transcript/features and libsndfile Opus fixture, no ffmpeg."""
import json
from pathlib import Path
import hashlib
import urllib.request
import numpy as np
import soundfile as sf
import torch
from gigaam.onnx_utils import load_onnx, infer_onnx
from hydra.utils import instantiate

root = Path(__file__).resolve().parents[1]
wav = root/'.tmp/gigaam-example.wav'
if not wav.exists():
    wav.parent.mkdir(parents=True, exist_ok=True)
    urllib.request.urlretrieve('https://cdn.chatwm.opensmodel.sberdevices.ru/GigaAM/example.wav', wav)
x, sr = sf.read(wav, dtype='float32')
assert sr == 16000 and x.ndim == 1
sf.write(root/'.tmp/gigaam-example.ogg', x, sr, format='OGG', subtype='OPUS')
sessions, cfg = load_onnx(str(root/'.tmp/gigaam-export'), 'v3_ctc', provider='CPUExecutionProvider')
# Array input avoids official load_audio(), which otherwise invokes ffmpeg.
text = infer_onnx([x], cfg, sessions, progress=False)[0].strip()
features, lengths = instantiate(cfg.preprocessor)(torch.from_numpy(x).unsqueeze(0), torch.tensor([len(x)]))
features.numpy().astype('<f4').tofile(root/'.tmp/gigaam-example.features.f32')
double_features, _ = instantiate(cfg.preprocessor).double()(torch.from_numpy(x).double().unsqueeze(0), torch.tensor([len(x)]))
rounding_error = (features.double()-double_features).abs().max().item()
result = dict(source='https://cdn.chatwm.opensmodel.sberdevices.ru/GigaAM/example.wav',
              wav_sha256=hashlib.sha256(wav.read_bytes()).hexdigest(),
              transcript=text, sample_rate=sr, duration_seconds=len(x)/sr,
              feature_shape=list(features.shape), feature_lengths=lengths.tolist(),
              official_float32_vs_float64_max_log_error=rounding_error,
              opus_encoder='soundfile/libsndfile in-process; no ffmpeg')
(root/'.tmp/gigaam-reference.json').write_text(json.dumps(result,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(result,ensure_ascii=True,indent=2))
