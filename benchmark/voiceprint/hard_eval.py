import json, itertools, statistics, numpy as np, soundfile as sf, torch, torchaudio, onnxruntime as ort
rng = np.random.default_rng(0); torch.set_num_threads(4)
A="/tmp/spk_bench/audio"; man=json.load(open(f"{A}/manifest.json"))
paths=[]; spk={}
for s,fs in man.items():
    for f in fs: p=f"{A}/{f}"; paths.append(p); spk[p]=s
def rd(p):
    x,sr=sf.read(p,dtype="float32"); return x if x.ndim==1 else x.mean(1)
wav={p:rd(p) for p in paths}

def rir(rt60=0.2, sr=16000):
    n=int(rt60*sr); t=np.arange(n)/sr
    h=rng.standard_normal(n)*np.exp(-6.9*t/rt60); h[0]=1.0
    return (h/np.sqrt((h**2).sum())).astype("float32")
H=rir()
def degrade(p):
    x=wav[p].copy()
    # 3s window from middle (or whole if shorter)
    n=3*16000
    if len(x)>n:
        st=(len(x)-n)//2; x=x[st:st+n]
    # reverb
    x=np.convolve(x,H)[:len(x)].astype("float32")
    # babble from 3 other-speaker clips
    others=[q for q in paths if spk[q]!=spk[p]]
    bab=np.zeros(len(x),dtype="float32")
    for q in rng.choice(others,3,replace=False):
        o=wav[q]
        if len(o)<len(x): o=np.tile(o,len(x)//len(o)+1)
        bab+=o[:len(x)]
    sx=np.sqrt((x**2).mean())+1e-9; sb=np.sqrt((bab**2).mean())+1e-9
    snr=8.0; bab*= (sx/sb)/(10**(snr/20))
    y=x+bab
    return (y/ (np.abs(y).max()+1e-9)).astype("float32")

hard={p:degrade(p) for p in paths}

def fbank(w):
    t=torch.from_numpy(w).unsqueeze(0)*32768.0
    f=torchaudio.compliance.kaldi.fbank(t,num_mel_bins=80,frame_length=25,frame_shift=10,sample_frequency=16000,dither=0.0)
    return (f-f.mean(0,keepdim=True)).unsqueeze(0).numpy().astype("float32")
ONNX={"ecapa":"ecapa512","resnet34":"resnet34","resnet293":"resnet293"}
def eer(embs):
    no=lambda v:(v:=np.asarray(v,float))/(np.linalg.norm(v)+1e-9)
    pos=[];neg=[]
    for a,b in itertools.combinations(paths,2):
        c=float(np.dot(no(embs[a]),no(embs[b]))); (pos if spk[a]==spk[b] else neg).append(c)
    P,N=len(pos),len(neg); best=9; e=0
    for t in np.linspace(-1,1,600):
        fa=sum(x>=t for x in neg)/N; fr=sum(x<t for x in pos)/P
        if abs(fa-fr)<best: best=abs(fa-fr); e=(fa+fr)/2
    return e*100, statistics.mean(pos), statistics.mean(neg)

print(f"{'model':10s} {'clean EER':>10s} {'HARD EER':>9s} {'same':>6s} {'diff':>6s}")
for name,fn in ONNX.items():
    s=ort.InferenceSession(f"/tmp/spk_bench/models/{fn}.onnx",providers=["CPUExecutionProvider"]); inp=s.get_inputs()[0].name
    emb=lambda w: s.run(None,{inp:fbank(w)})[0].reshape(-1)
    ce,_,_=eer({p:emb(wav[p]) for p in paths})
    he,mp,mn=eer({p:emb(hard[p]) for p in paths})
    print(f"{name:10s} {ce:9.2f}% {he:8.2f}% {mp:6.3f} {mn:6.3f}")
try:
    from resemblyzer import VoiceEncoder, preprocess_wav
    enc=VoiceEncoder("cpu")
    ce,_,_=eer({p:enc.embed_utterance(preprocess_wav(wav[p],source_sr=16000)) for p in paths})
    he,mp,mn=eer({p:enc.embed_utterance(preprocess_wav(hard[p],source_sr=16000)) for p in paths})
    print(f"{'resembly':10s} {ce:9.2f}% {he:8.2f}% {mp:6.3f} {mn:6.3f}")
except Exception as ex: print("resemblyzer skip:",ex)
print("\ncondition: 3s turns + reverb(RT60 0.2s) + babble@8dB SNR (3 interferers)")
