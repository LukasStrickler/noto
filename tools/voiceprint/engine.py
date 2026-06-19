"""Voiceprint engine: ECAPA-TDNN-512 (WeSpeaker ONNX) + SQLite multi-exemplar gallery.

This is the real identity layer for noto's speaker-ID feature (.docs/speaker-identity.md).
CPU-only, no GPU. One model: `ecapa` (192-d), the benchmarked default (benchmark/voiceprint/).

The gallery owns identity: profiles (people), a multi-exemplar embedding gallery, and
links (meeting-speaker -> profile). Matching = max-cosine over a profile's exemplars with
a per-meeting uniqueness constraint and a speech-duration quality gate.
"""
from __future__ import annotations
import os, sqlite3, struct, uuid, time, json
import numpy as np

SR = 16000
DEFAULT_MODEL = os.path.join(os.path.dirname(__file__), "models", "ecapa512.onnx")

# Decision thresholds (cosine). Per-model; calibrate on real data (bench_precision.py).
# Scoring is centroid-based (mean query vec vs profile centroid), so these sit on the
# same scale as the cross-clip verification EER, not the inflated max-over-exemplars scale.
AUTO = 0.50          # >= AUTO and unique  -> auto-confirmed match
PENDING = 0.40       # >= PENDING          -> suggested (needs confirm)
MIN_WINDOW_S = 1.5   # drop turns shorter than this before embedding
ENROLL_MIN_S = 2.0   # clean speech needed to AUTO-confirm / extend a profile gallery
MAX_EXEMPLARS = 12   # gallery cap per profile


def _fbank(wav: np.ndarray):
    import torch, torchaudio
    t = torch.from_numpy(wav.astype("float32")).unsqueeze(0) * 32768.0
    f = torchaudio.compliance.kaldi.fbank(
        t, num_mel_bins=80, frame_length=25, frame_shift=10,
        sample_frequency=SR, dither=0.0)
    return (f - f.mean(0, keepdim=True)).unsqueeze(0).numpy().astype("float32")


def _norm(v: np.ndarray) -> np.ndarray:
    v = np.asarray(v, dtype="float32")
    n = float(np.linalg.norm(v))
    return v / (n + 1e-9)


class Engine:
    def __init__(self, db_path: str, model_path: str = DEFAULT_MODEL,
                 auto: float = AUTO, pending: float = PENDING,
                 enroll_min_s: float = ENROLL_MIN_S):
        import onnxruntime as ort
        so = ort.SessionOptions()
        so.intra_op_num_threads = 4
        so.inter_op_num_threads = 1
        self.sess = ort.InferenceSession(model_path, sess_options=so,
                                         providers=["CPUExecutionProvider"])
        self.inp = self.sess.get_inputs()[0].name
        self.model_id = "ecapa"
        self.dim = 192
        self.auto = auto
        self.pending = pending
        self.enroll_min_s = enroll_min_s
        self.db = sqlite3.connect(db_path)
        self._init_db()

    # ---- model ----
    def embed(self, wav: np.ndarray) -> np.ndarray:
        """One L2-normalized 192-d embedding for a mono 16k window."""
        out = self.sess.run(None, {self.inp: _fbank(wav)})[0].reshape(-1)
        return _norm(out)

    def embed_turns(self, wav: np.ndarray, turns):
        """Embed a speaker's turns -> (exemplars[list of 192-d], speech_seconds).

        Turns are (start_s, end_s). Drops <MIN_WINDOW_S; embeds the longest turns
        first up to MAX_EXEMPLARS; long turns are split into <=8 s windows.
        """
        clean = [(a, b) for a, b in turns if b - a >= MIN_WINDOW_S]
        clean.sort(key=lambda ab: ab[1] - ab[0], reverse=True)
        exemplars, secs = [], 0.0
        for a, b in clean:
            s0 = int(a * SR)
            for w0 in range(s0, int(b * SR), 8 * SR):
                w1 = min(int(b * SR), w0 + 8 * SR)
                if (w1 - w0) / SR < MIN_WINDOW_S:
                    continue
                seg = wav[w0:w1]
                if len(seg) < int(MIN_WINDOW_S * SR):
                    continue
                exemplars.append(self.embed(seg))
                secs += (w1 - w0) / SR
                if len(exemplars) >= MAX_EXEMPLARS:
                    return exemplars, secs
        return exemplars, secs

    # ---- gallery (SQLite) ----
    def _init_db(self):
        self.db.executescript("""
        CREATE TABLE IF NOT EXISTS profiles(
          id TEXT PRIMARY KEY, display_name TEXT, model_id TEXT,
          created_at REAL, last_seen_at REAL);
        CREATE TABLE IF NOT EXISTS embeddings(
          id TEXT PRIMARY KEY, profile_id TEXT, model_id TEXT, dim INTEGER,
          vector BLOB, meeting_id TEXT, speaker_id TEXT, seconds REAL, created_at REAL);
        CREATE TABLE IF NOT EXISTS links(
          meeting_id TEXT, speaker_id TEXT, profile_id TEXT, status TEXT,
          confidence REAL, decided_at REAL, PRIMARY KEY(meeting_id, speaker_id));
        """)
        self.db.commit()

    def _profiles(self):
        """-> [(profile_id, name, centroid)] where centroid = normalized mean exemplar."""
        rows = self.db.execute(
            "SELECT id, display_name FROM profiles WHERE model_id=?", (self.model_id,)
        ).fetchall()
        out = []
        for pid, name in rows:
            ex = [np.frombuffer(b, dtype="float32")
                  for (b,) in self.db.execute(
                      "SELECT vector FROM embeddings WHERE profile_id=?", (pid,))]
            if ex:
                out.append((pid, name, _norm(np.vstack(ex).mean(0))))
        return out

    @staticmethod
    def _query_vec(query_exemplars) -> np.ndarray:
        """A speaker's exemplars -> one normalized query vector (mean)."""
        return _norm(np.vstack(query_exemplars).mean(0))

    def _score(self, query_vec, centroid) -> float:
        """Cosine of the speaker's mean vector vs a profile centroid.

        Centroid scoring keeps imposter scores on the same scale as the cross-clip
        verification EER (unlike max-over-exemplars, which inflates the imposter tail).
        Both sides are L2-normalized, so dot == cosine.
        """
        return float(query_vec @ centroid)

    def _new_profile(self, name=None) -> str:
        pid = "p_" + uuid.uuid4().hex[:12]
        self.db.execute(
            "INSERT INTO profiles(id,display_name,model_id,created_at,last_seen_at)"
            " VALUES(?,?,?,?,?)", (pid, name, self.model_id, time.time(), time.time()))
        return pid

    def _add_exemplars(self, pid, exemplars, meeting_id, speaker_id, seconds):
        for v in exemplars[:MAX_EXEMPLARS]:
            self.db.execute(
                "INSERT INTO embeddings(id,profile_id,model_id,dim,vector,meeting_id,"
                "speaker_id,seconds,created_at) VALUES(?,?,?,?,?,?,?,?,?)",
                ("e_" + uuid.uuid4().hex[:12], pid, self.model_id, self.dim,
                 v.astype("float32").tobytes(), meeting_id, speaker_id,
                 seconds, time.time()))
        # cap exemplars: keep newest MAX_EXEMPLARS
        ids = [r[0] for r in self.db.execute(
            "SELECT id FROM embeddings WHERE profile_id=? ORDER BY created_at DESC", (pid,))]
        for stale in ids[MAX_EXEMPLARS:]:
            self.db.execute("DELETE FROM embeddings WHERE id=?", (stale,))
        self.db.execute("UPDATE profiles SET last_seen_at=? WHERE id=?", (time.time(), pid))

    def name_profile(self, pid, name):
        self.db.execute("UPDATE profiles SET display_name=? WHERE id=?", (name, pid))
        self.db.commit()

    # ---- the identify operation (one meeting) ----
    def identify_meeting(self, meeting_id, speakers, wav, enroll=True):
        """speakers: {speaker_id: [(start_s,end_s), ...]}. Returns assignments.

        Greedy unique assignment: rank all (speaker, profile) candidate scores,
        assign best-first, never reusing a profile within the meeting. Below
        PENDING -> a fresh profile (subject to the enroll quality gate).
        """
        # embed every meeting speaker -> one query vector + speech seconds
        q = {}
        for sid, turns in speakers.items():
            ex, secs = self.embed_turns(wav, turns)
            q[sid] = (self._query_vec(ex) if ex else None, secs, ex)
        gallery = self._profiles()
        # candidate (speaker, profile) scores, best first
        cands = []
        for sid, (qv, secs, ex) in q.items():
            if qv is None:
                continue
            for pid, name, centroid in gallery:
                cands.append((self._score(qv, centroid), sid, pid, name))
        cands.sort(reverse=True)
        assigned_spk, used_prof, result = {}, set(), {}
        for score, sid, pid, name in cands:
            if sid in assigned_spk or pid in used_prof:
                continue          # in-meeting uniqueness: 1 speaker <-> 1 profile
            if score < self.pending:
                continue
            status = "auto" if score >= self.auto else "pending"
            assigned_spk[sid] = pid
            used_prof.add(pid)
            result[sid] = dict(profile_id=pid, name=name, score=round(score, 3),
                               status=status)
        # leftover speakers with usable speech -> mint a new profile
        for sid, (qv, secs, ex) in q.items():
            if sid in assigned_spk:
                continue
            if qv is None:
                result[sid] = dict(profile_id=None, name=None, score=0.0,
                                   status="too_short")
                continue
            if enroll:
                pid = self._new_profile()
                result[sid] = dict(profile_id=pid, name=None, score=0.0, status="new")
                assigned_spk[sid] = pid
                used_prof.add(pid)
            else:
                result[sid] = dict(profile_id=None, name=None, score=0.0,
                                   status="unmatched")
        # online enrollment: extend the gallery for confident matches + new profiles
        if enroll:
            for sid, pid in assigned_spk.items():
                qv, secs, ex = q[sid]
                st = result[sid]["status"]
                if st in ("auto", "new") and secs >= self.enroll_min_s:
                    self._add_exemplars(pid, ex, meeting_id, sid, secs)
                self.db.execute(
                    "INSERT OR REPLACE INTO links(meeting_id,speaker_id,profile_id,"
                    "status,confidence,decided_at) VALUES(?,?,?,?,?,?)",
                    (meeting_id, sid, pid, st, result[sid]["score"], time.time()))
            self.db.commit()
        return result

    def stats(self):
        p = self.db.execute("SELECT COUNT(*) FROM profiles").fetchone()[0]
        e = self.db.execute("SELECT COUNT(*) FROM embeddings").fetchone()[0]
        return dict(profiles=p, exemplars=e, model=self.model_id, dim=self.dim)
