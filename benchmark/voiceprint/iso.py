import sys, time, json, statistics, resource, numpy as np, soundfile as sf, glob
import torch, torchaudio
torch.set_num_threads(4)
name=sys.argv[1]
A="/tmp/spk_bench/audio"
import os
files=sorted(glob.glob(f"{A}/*.flac"))[:48]
wavs=[]
for f in files:
    x,sr=sf.read(f,dtype="float32"); wavs.append(x if x.ndim==1 else x.mean(1))
meeting=np.concatenate(wavs);
seg3=meeting[:48000]
def fbank(w):
    t=torch.from_numpy(w).unsqueeze(0)*32768.0
    f=torchaudio.compliance.kaldi.fbank(t,num_mel_bins=80,frame_length=25,frame_shift=10,sample_frequency=16000,dither=0.0)
    return (f-f.mean(0,keepdim=True)).unsqueeze(0).numpy().astype("float32")
if name=="resemblyzer":
    from resemblyzer import VoiceEncoder
    t0=time.perf_counter(); enc=VoiceEncoder("cpu"); load=(time.perf_counter()-t0)*1000
    emb=lambda w: enc.embed_utterance(w)
else:
    import onnxruntime as ort
    so=ort.SessionOptions(); so.intra_op_num_threads=4; so.inter_op_num_threads=1
    fn={"ecapa":"ecapa512","resnet34":"resnet34","resnet293":"resnet293","campp":"campplus"}[name]
    t0=time.perf_counter(); s=ort.InferenceSession(f"/tmp/spk_bench/models/{fn}.onnx",sess_options=so,providers=["CPUExecutionProvider"]); load=(time.perf_counter()-t0)*1000
    inp=s.get_inputs()[0].name
    emb=lambda w: s.run(None,{inp:fbank(w)})[0].reshape(-1)
for _ in range(5): emb(seg3)
# median single 3s-segment latency
ts=[]
for _ in range(15):
    t0=time.perf_counter(); emb(seg3); ts.append((time.perf_counter()-t0)*1000)
lat3=statistics.median(ts)
# sustained throughput over meeting in 3s windows
step=48000; n=0; t0=time.perf_counter()
for s0 in range(0,len(meeting)-step,step):
    emb(meeting[s0:s0+step]); n+=1
wall=time.perf_counter()-t0
peak_rss=resource.getrusage(resource.RUSAGE_SELF).ru_maxrss/1024.0  # MB
print(json.dumps(dict(model=name,load_ms=round(load),lat3_ms=round(lat3,1),
      seg_per_s=round(n/wall,1),rtf=round(wall/(n*3),4),peak_rss_mb=round(peak_rss),
      meeting_s=round(len(meeting)/16000,1),windows=n)))
