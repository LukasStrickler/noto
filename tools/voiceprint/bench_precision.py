"""Precision harness for the voiceprint engine.

Builds ground-truth "meetings" from a labeled speaker pool (the same people recur
across meetings, using different clips), runs the engine meeting-by-meeting with
online enrollment, and measures cross-meeting identity linking:

  - cross-clip verification EER (calibration: where should the thresholds sit?)
  - end-to-end clustering precision / recall / F1 (pairwise)
  - returning-speaker link accuracy + false merges + #profiles vs #true speakers
"""
import os, sys, json, itertools, tempfile, collections
import numpy as np, soundfile as sf
sys.path.insert(0, os.path.dirname(__file__))
from engine import Engine, SR

AUDIO = sys.argv[1] if len(sys.argv) > 1 else os.path.join(
    os.path.dirname(__file__), "..", "..", "bench", "meetings", "librispeech")
man = json.load(open(os.path.join(AUDIO, "manifest.json")))
spk_clips = {s: [os.path.join(AUDIO, f) for f in fs] for s, fs in man.items()}
speakers = sorted(spk_clips)


def rd(p):
    x, sr = sf.read(p, dtype="float32")
    return x if x.ndim == 1 else x.mean(1)


wav = {p: rd(p) for s in speakers for p in spk_clips[s]}

# ---------- 1. calibration: cross-clip verification EER ----------
eng = Engine(db_path=":memory:")
clip_emb = {p: eng.embed(wav[p]) for s in speakers for p in spk_clips[s]}
true_of = {p: s for s in speakers for p in spk_clips[s]}
pos, neg = [], []
for a, b in itertools.combinations(clip_emb, 2):
    c = float(clip_emb[a] @ clip_emb[b])
    (pos if true_of[a] == true_of[b] else neg).append(c)
pos, neg = np.array(pos), np.array(neg)
best = (9, 0)
for t in np.linspace(0, 1, 400):
    fa = (neg >= t).mean(); fr = (pos < t).mean()
    if abs(fa - fr) < abs(best[0] - best[1]):
        best = (fa, fr); thr = t
eer = (best[0] + best[1]) / 2 * 100
print("=== calibration (cross-clip verification) ===")
print(f"same-cos mean={pos.mean():.3f}  diff-cos mean={neg.mean():.3f}")
print(f"EER={eer:.2f}%  EER-threshold={thr:.3f}  (pos={len(pos)} neg={len(neg)})")
# a safe auto-threshold ~ midway between EER point and the same-speaker mean
print(f"suggested PENDING≈{thr:.2f}  AUTO≈{(thr+pos.mean())/2:.2f}\n")

# ---------- 2. build meetings (same speakers recur) ----------
# speaker s contributes its 4 clips to 4 different meetings -> a recurring regular.
M = 8
meetings = collections.defaultdict(list)   # mid -> [(true_spk, clip_path)]
for si, s in enumerate(speakers):
    for ci, p in enumerate(spk_clips[s]):
        mid = (si + ci) % M
        meetings[mid].append((s, p))

# pre-build meeting wavs + turns once (so the sweep only re-runs matching)
built = []
for mid in range(M):
    buf, turns, t, labels = [], collections.defaultdict(list), 0.0, []
    for idx, (s, p) in enumerate(meetings[mid]):
        x = wav[p]; dur = len(x) / SR; sid = f"spk_{idx}"
        buf.append(x); turns[sid].append((t, t + dur)); t += dur
        buf.append(np.zeros(int(0.3 * SR), "float32")); t += 0.3
        labels.append((s, sid))
    built.append((np.concatenate(buf), dict(turns), labels))


def run(auto, pending):
    eng = Engine(db_path=":memory:", auto=auto, pending=pending)
    instances = []
    for mid, (mwav, turns, labels) in enumerate(built):
        res = eng.identify_meeting(f"m{mid}", turns, mwav, enroll=True)
        for s, sid in labels:
            instances.append((mid, s, res[sid]["profile_id"]))
    # pairwise clustering metrics
    tp = fp = fn = 0
    for (m1, s1, p1), (m2, s2, p2) in itertools.combinations(instances, 2):
        same_true, same_pred = (s1 == s2), (p1 is not None and p1 == p2)
        if same_true and same_pred: tp += 1
        elif same_true and not same_pred: fn += 1
        elif not same_true and same_pred: fp += 1
    P = tp / (tp + fp) if tp + fp else 1.0
    R = tp / (tp + fn) if tp + fn else 1.0
    F1 = 2 * P * R / (P + R) if P + R else 0.0
    first, rt, ro = {}, 0, 0
    for m, s, pid in instances:
        if s in first:
            rt += 1; ro += (pid is not None and pid == first[s])
        else:
            first[s] = pid
    byprof = collections.defaultdict(set)
    for m, s, pid in instances:
        if pid is not None: byprof[pid].add(s)
    fm = sum(1 for v in byprof.values() if len(v) > 1)
    return dict(P=P, R=R, F1=F1, profiles=len(byprof), false_merges=fm,
                ret=f"{ro}/{rt}", ret_pct=(ro/rt*100 if rt else 0))


print("=== end-to-end cross-meeting identity (threshold sweep) ===")
print(f"meetings={M}  instances={sum(len(b[2]) for b in built)}  true_speakers={len(speakers)}  (ideal profiles={len(speakers)})")
print(f"{'PENDING':>7s} {'AUTO':>5s} {'prec':>6s} {'recall':>6s} {'F1':>6s} {'profs':>5s} {'merges':>6s} {'returning':>10s}")
grid = [(0.40, 0.50), (0.45, 0.55), (0.50, 0.58), (0.50, 0.62), (0.55, 0.65)]
best = None
for pen, au in grid:
    r = run(au, pen)
    print(f"{pen:7.2f} {au:5.2f} {r['P']:6.3f} {r['R']:6.3f} {r['F1']:6.3f} "
          f"{r['profiles']:5d} {r['false_merges']:6d} {r['ret']:>7s} {r['ret_pct']:4.0f}%")
    if best is None or r['F1'] > best[1]['F1']:
        best = ((pen, au), r)
(pen, au), r = best
print(f"\nbest F1 @ PENDING={pen} AUTO={au}: precision={r['P']:.3f} recall={r['R']:.3f} "
      f"F1={r['F1']:.3f} profiles={r['profiles']} false_merges={r['false_merges']} "
      f"returning={r['ret']} ({r['ret_pct']:.0f}%)")
