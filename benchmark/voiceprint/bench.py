import os, json, time, glob, statistics, resource, itertools, math
import numpy as np
import soundfile as sf
import onnxruntime as ort
import torch, torchaudio
torch.set_num_threads(4)

AUDIO = "/tmp/spk_bench/audio"
MODELS = "/tmp/spk_bench/models"
THREADS = 4

def vmrss_mb():
    for line in open("/proc/self/status"):
        if line.startswith("VmRSS"):
            return int(line.split()[1]) / 1024.0
    return 0.0

def read_wav(path):
    x, sr = sf.read(path, dtype="float32")
    if x.ndim > 1: x = x.mean(1)
    if sr != 16000:
        x = torchaudio.functional.resample(torch.from_numpy(x), sr, 16000).numpy()
    return x.astype("float32")

def fbank(wav):  # WeSpeaker frontend: 80-d kaldi fbank, CMN
    t = torch.from_numpy(wav).unsqueeze(0) * (1 << 15)
    feat = torchaudio.compliance.kaldi.fbank(
        t, num_mel_bins=80, frame_length=25, frame_shift=10,
        sample_frequency=16000, dither=0.0)
    feat = feat - feat.mean(0, keepdim=True)
    return feat.unsqueeze(0).numpy().astype("float32")  # [1,T,80]

# ---- load manifest / audio ----
manifest = json.load(open(f"{AUDIO}/manifest.json"))
clips = {}  # path -> wav
spk_of = {}
for spk, files in manifest.items():
    for f in files:
        p = f"{AUDIO}/{f}"
        clips[p] = read_wav(p)
        spk_of[p] = spk
paths = list(clips.keys())
durs = {p: len(clips[p]) / 16000 for p in paths}
print(f"clips={len(paths)} speakers={len(manifest)} "
      f"median_dur={statistics.median(durs.values()):.1f}s "
      f"total_audio={sum(durs.values())/60:.1f}min")

# ---- build a ~meeting clip for throughput ----
order = sorted(paths)[:48]
meeting = np.concatenate([clips[p] for p in order])
meeting_dur = len(meeting) / 16000
print(f"meeting clip = {meeting_dur:.1f}s\n")

def make_sess(path):
    so = ort.SessionOptions()
    so.intra_op_num_threads = THREADS
    so.inter_op_num_threads = 1
    return ort.InferenceSession(path, sess_options=so, providers=["CPUExecutionProvider"])

ONNX = {
    "ecapa(512)":   "ecapa512.onnx",
    "campp":        "campplus.onnx",
    "resnet34":     "resnet34.onnx",
    "resnet293":    "resnet293.onnx",
}

results = {}

def slice_secs(wav, L):
    n = int(L * 16000)
    return wav[:n] if len(wav) >= n else np.pad(wav, (0, n - len(wav)))

for name, fn in ONNX.items():
    rss0 = vmrss_mb()
    t0 = time.perf_counter()
    sess = make_sess(f"{MODELS}/{fn}")
    load_ms = (time.perf_counter() - t0) * 1000
    rss1 = vmrss_mb()
    inp = sess.get_inputs()[0].name
    def embed(wav):
        out = sess.run(None, {inp: fbank(wav)})[0]
        return np.asarray(out).reshape(-1)
    dim = embed(clips[paths[0]]).shape[0]
    # warmup
    for _ in range(3): embed(slice_secs(meeting, 3))
    # latency vs segment length (median of N)
    lat = {}
    for L in (1, 2, 3, 5, 10):
        seg = slice_secs(meeting, L)
        ts = []
        for _ in range(7):
            t0 = time.perf_counter(); embed(seg); ts.append((time.perf_counter()-t0)*1000)
        lat[L] = statistics.median(ts)
    # throughput: embed whole meeting in 3s windows
    win = 3; step = int(win*16000); n=0
    t0 = time.perf_counter()
    for s in range(0, len(meeting)-step, step):
        embed(meeting[s:s+step]); n+=1
    wall = time.perf_counter()-t0
    segs_per_s = n/wall
    rtf = wall/ (n*win)   # processing time / audio time
    # embeddings for EER
    embs = {p: embed(clips[p]) for p in paths}
    results[name] = dict(load_ms=load_ms, rss_mb=rss1-rss0, dim=dim,
                         lat=lat, segs_per_s=segs_per_s, rtf=rtf, embs=embs,
                         size_mb=os.path.getsize(f"{MODELS}/{fn}")/1e6)
    del sess
    print(f"{name:12s} dim={dim:4d} load={load_ms:6.0f}ms rss+={rss1-rss0:5.0f}MB "
          f"lat3s={lat[3]:6.1f}ms thr={segs_per_s:5.1f}seg/s rtf={rtf:.4f}")

# ---- resemblyzer ----
try:
    from resemblyzer import VoiceEncoder, preprocess_wav
    rss0 = vmrss_mb(); t0=time.perf_counter()
    enc = VoiceEncoder("cpu")
    load_ms=(time.perf_counter()-t0)*1000; rss1=vmrss_mb()
    def rembed_wav(wav):
        return enc.embed_utterance(wav)
    pre = {p: preprocess_wav(p) for p in paths}
    for _ in range(3): rembed_wav(pre[paths[0]])
    lat={}
    for L in (1,2,3,5,10):
        seg = slice_secs(meeting, L)  # resemblyzer preprocess expects 16k float
        ts=[]
        for _ in range(7):
            t0=time.perf_counter(); rembed_wav(seg); ts.append((time.perf_counter()-t0)*1000)
        lat[L]=statistics.median(ts)
    win=3; step=int(win*16000); n=0; t0=time.perf_counter()
    for s in range(0,len(meeting)-step,step):
        rembed_wav(meeting[s:s+step]); n+=1
    wall=time.perf_counter()-t0; segs_per_s=n/wall; rtf=wall/(n*win)
    embs={p: rembed_wav(pre[p]) for p in paths}
    dim=len(embs[paths[0]])
    results["resemblyzer"]=dict(load_ms=load_ms,rss_mb=rss1-rss0,dim=dim,lat=lat,
                                segs_per_s=segs_per_s,rtf=rtf,embs=embs,size_mb=17.0)
    print(f"{'resemblyzer':12s} dim={dim:4d} load={load_ms:6.0f}ms rss+={rss1-rss0:5.0f}MB "
          f"lat3s={lat[3]:6.1f}ms thr={segs_per_s:5.1f}seg/s rtf={rtf:.4f}")
except Exception as e:
    print("resemblyzer FAILED:", e)

# ---- EER / AUC ----
def norm(v):
    v=np.asarray(v,dtype="float64"); return v/(np.linalg.norm(v)+1e-9)
def eer_auc(embs):
    pos=[]; neg=[]
    for p,q in itertools.combinations(paths,2):
        c=float(np.dot(norm(embs[p]),norm(embs[q])))
        (pos if spk_of[p]==spk_of[q] else neg).append(c)
    # subsample negatives for speed of sweep
    neg_s = neg
    scores=[(s,1) for s in pos]+[(s,0) for s in neg_s]
    # EER via threshold sweep
    thr=np.linspace(-1,1,400); best=(1,0)
    P=len(pos); N=len(neg_s)
    for t in thr:
        fa=sum(1 for s in neg_s if s>=t)/N
        fr=sum(1 for s in pos if s< t)/P
        if abs(fa-fr)<abs(best[0]-best[1]): best=(fa,fr); bt=t
    eer=(best[0]+best[1])/2*100
    # AUC
    order=sorted(scores,key=lambda x:x[0])
    ranks={};
    s_sorted=[s for s,_ in order]
    # Mann-Whitney AUC
    allv=sorted([s for s in pos+neg_s])
    import bisect
    auc=0.0
    for s in pos:
        auc+=(bisect.bisect_left(sorted(neg_s),s))/N  # frac negs below this pos
    auc/=P
    return eer, auc, statistics.mean(pos), statistics.mean(neg_s), P, N

print("\n=== discrimination (LibriSpeech clean; lower EER better) ===")
for name,r in results.items():
    eer,auc,mp,mn,P,N=eer_auc(r["embs"])
    r["eer"]=eer; r["auc"]=auc; r["pos"]=mp; r["neg"]=mn
    print(f"{name:12s} EER={eer:5.2f}%  AUC={auc:.4f}  cos same={mp:.3f} diff={mn:.3f}  (pos={P} neg={N})")

# ---- summary table ----
print("\n=== SUMMARY ===")
print(f"{'model':12s} {'dim':>4s} {'size':>6s} {'load':>7s} {'ram':>6s} "
      f"{'1s':>6s} {'3s':>6s} {'10s':>7s} {'seg/s':>6s} {'RTF':>7s} {'EER%':>6s}")
for name,r in results.items():
    l=r["lat"]
    print(f"{name:12s} {r['dim']:4d} {r['size_mb']:5.0f}M {r['load_ms']:6.0f}m "
          f"{r['rss_mb']:5.0f}M {l[1]:5.1f} {l[3]:5.1f} {l[10]:6.1f} "
          f"{r['segs_per_s']:5.1f} {r['rtf']:.4f} {r['eer']:5.2f}")
json.dump({k:{kk:vv for kk,vv in v.items() if kk!='embs'} for k,v in results.items()},
          open("/tmp/spk_bench/results.json","w"), indent=1, default=float)
print("\nsaved -> /tmp/spk_bench/results.json")
