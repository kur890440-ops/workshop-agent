"""DEV ONLY: package official GigaAM export and generate reference fixtures."""
import argparse, hashlib, json, wave
from pathlib import Path
FRONTEND = dict(sample_rate=16000,n_fft=320,win_length=320,hop_length=160,features=64,center=False,mel_scale='htk',power=2,window='hann_periodic',normalization='log_clamp_1e-9_1e9')
def manifest(dest):
    hashes={}
    for name in ('model.onnx','config.json','vocab.json'):
        with (dest/name).open('rb') as f: hashes[name]=hashlib.file_digest(f,'sha256').hexdigest()
    (dest/'manifest.json').write_text(json.dumps(hashes,indent=2),encoding='utf-8')
def config(dest,vocab,inputs,outputs):
    (dest/'config.json').write_text(json.dumps(dict(version=1,model='v3_ctc',frontend=FRONTEND,blank_id=len(vocab),tokenizer='charwise',inputs=inputs,outputs=outputs)),encoding='utf-8')
    (dest/'vocab.json').write_text(json.dumps(vocab,ensure_ascii=False),encoding='utf-8')
    manifest(dest)
def package(export,dest):
    import onnx
    from omegaconf import OmegaConf
    cfg=OmegaConf.load(export/'v3_ctc.yaml')
    assert cfg.model_name=='v3_ctc'
    for key in ('sample_rate','n_fft','win_length','hop_length','features','center'): assert cfg.preprocessor[key]==FRONTEND[key],key
    assert cfg.preprocessor.get('mel_scale','htk')=='htk' and cfg.preprocessor.get('mel_norm') is None
    assert not cfg.decoding.get('model_path'),'Only charwise v3_ctc supported'
    vocab=list(cfg.decoding.vocabulary);assert len(vocab)==33
    model=onnx.load(export/'v3_ctc.onnx');onnx.external_data_helper.convert_model_from_external_data(model)
    inputs=[x.name for x in model.graph.input];outputs=[x.name for x in model.graph.output]
    assert inputs==['features','feature_lengths'], inputs
    assert outputs==['log_probs','encoded_lengths'], outputs
    def describe(value):
        t=value.type.tensor_type
        return dict(name=value.name,dtype=onnx.TensorProto.DataType.Name(t.elem_type),
                    shape=[d.dim_value if d.HasField('dim_value') else d.dim_param for d in t.shape.dim])
    diagnostics=dict(inputs=[describe(v) for v in model.graph.input],outputs=[describe(v) for v in model.graph.output])
    print(json.dumps(diagnostics,indent=2),flush=True)
    for value, dtype, rank in zip(list(model.graph.input)+list(model.graph.output),[(1,),(7,),(1,),(6,7)],[3,1,3,1]):
        assert value.type.tensor_type.elem_type in dtype, value.name
        assert len(value.type.tensor_type.shape.dim)==rank, value.name
    assert model.graph.input[0].type.tensor_type.shape.dim[1].dim_value==64
    assert model.graph.output[0].type.tensor_type.shape.dim[2].dim_value==len(vocab)+1
    onnx.checker.check_model(model)
    dest.mkdir(parents=True,exist_ok=True);onnx.save_model(model,dest/'model.onnx')
    config(dest,vocab,inputs,outputs)
    (dest/'reference-config.yaml').write_bytes((export/'v3_ctc.yaml').read_bytes())
    (dest/'onnx-contract.json').write_text(json.dumps(diagnostics,indent=2),encoding='utf-8')
def golden(dest, reference):
    import numpy as np
    import torch,torchaudio
    dest.mkdir(parents=True,exist_ok=True)
    pcm=np.random.default_rng(260026).integers(-12000,12000,size=16000,dtype=np.int16)
    with wave.open(str(dest/'frontend.wav'),'wb') as f:
        f.setnchannels(1);f.setsampwidth(2);f.setframerate(16000);f.writeframes(pcm.tobytes())
    x=torch.from_numpy(pcm.astype(np.float32)/32768).unsqueeze(0)
    import importlib.util
    spec=importlib.util.spec_from_file_location('official_preprocess',reference)
    module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
    transform=module.FeatureExtractor(sample_rate=16000,features=64,n_fft=320,win_length=320,hop_length=160,center=False)
    result,length=transform(x,torch.tensor([x.shape[1]]))
    assert length.tolist()==[99]
    result.numpy().astype('<f4').tofile(dest/'frontend.f32')
    (dest/'frontend.json').write_text(json.dumps(dict(frontend=FRONTEND,shape=list(result.shape),torch=torch.__version__,torchaudio=torchaudio.__version__,source='https://github.com/salute-developers/GigaAM/blob/main/gigaam/preprocess.py',source_sha256=hashlib.sha256(reference.read_bytes()).hexdigest(),pcm_sha256=hashlib.sha256(pcm.tobytes()).hexdigest(),absolute_tolerance=0.0002),indent=2),encoding='utf-8')
def native_fixture(dest, cancellation=False):
    import onnx
    from onnx import helper as h,TensorProto as T
    dest.mkdir(parents=True,exist_ok=True);logits=[0.]*102
    for t,k in enumerate([1,33,1]):logits[t*34+k]=1.
    graph=h.make_graph([h.make_node('Constant',[],['log_probs'],value=h.make_tensor('logits',T.FLOAT,[1,3,34],logits)),h.make_node('Constant',[],['encoded_lengths'],value=h.make_tensor('length',T.INT64,[1],[3]))],'speech-ffi-fixture',[h.make_tensor_value_info('features',T.FLOAT,[1,64,'time']),h.make_tensor_value_info('feature_lengths',T.INT64,[1])],[h.make_tensor_value_info('log_probs',T.FLOAT,[1,3,34]),h.make_tensor_value_info('encoded_lengths',T.INT64,[1])])
    if cancellation:
        body=h.make_graph([h.make_node('Identity',['cond'],['next_cond']),h.make_node('Add',['value','one'],['next_value'])],'loop-body',[h.make_tensor_value_info('iter',T.INT64,[]),h.make_tensor_value_info('cond',T.BOOL,[]),h.make_tensor_value_info('value',T.FLOAT,[])],[h.make_tensor_value_info('next_cond',T.BOOL,[]),h.make_tensor_value_info('next_value',T.FLOAT,[])],[h.make_tensor('one',T.FLOAT,[],[1.])])
        graph.node[0].output[0]='base_logits'
        graph.initializer.extend([h.make_tensor('trip',T.INT64,[],[1000000000]),h.make_tensor('condition',T.BOOL,[],[True]),h.make_tensor('initial',T.FLOAT,[],[0.])])
        graph.node.extend([h.make_node('Loop',['trip','condition','initial'],['sum'],body=body),h.make_node('Add',['base_logits','sum'],['log_probs'])])
    onnx.save(h.make_model(graph,opset_imports=[h.make_opsetid('',17)],ir_version=9),dest/'model.onnx')
    config(dest,list(' '+''.join(chr(i) for i in range(1072,1104))),['features','feature_lengths'],['log_probs','encoded_lengths'])
if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('action',choices=['package','golden','native-fixture','native-cancellation']);p.add_argument('destination',type=Path);p.add_argument('--export',type=Path);p.add_argument('--reference',type=Path);a=p.parse_args()
    if a.action=='package':
        if not a.export:p.error('--export required')
        package(a.export,a.destination)
    elif a.action=='golden':
        if not a.reference:p.error('--reference path/to/GigaAM/gigaam/preprocess.py required')
        golden(a.destination,a.reference)
    else:native_fixture(a.destination,a.action=='native-cancellation')
