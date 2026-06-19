#!/usr/bin/env python3
"""Convert the fp32 Parakeet-TDT transducer (encoder/decoder/joiner) to fp16 ONNX.

WHY: on the GPU, int8 is a CPU-only optimization (the CUDA EP inserts
quantize/dequantize nodes and falls back to fp32 compute — measured 3.6× SLOWER,
see modal_benchmark.py). fp16 is the real GPU lever: ONNX-Runtime's CUDA EP has
native fp16 kernels, so an fp16 export cuts the dominant STT FLOP bucket ~1.5–2×.
Upstream publishes no fp16 Parakeet export, so we make one from the fp32 model.

SAFETY (this is a numerically delicate FastConformer transducer):
  * keep_io_types=True — every model's external inputs/outputs stay float32, so
    the encoder→decoder→joiner handoff that sherpa-onnx wires in C++ is fp32 and
    only the INTERNAL compute is fp16. Mixing fp16 across the model boundary would
    feed fp16 where sherpa expects fp32.
  * op_block_list (default: LayerNormalization, Softmax) keeps the overflow-prone
    ops in fp32. Attention softmax and layernorm are cheap, so blocking them barely
    dents the speedup but avoids fp16 inf/NaN. Override via PARAKEET_FP16_BLOCK
    (comma-separated op types) to widen/narrow the block list on a follow-up run.
  * values are clamped to the fp16 finite range by the converter (no inf).

TWO non-obvious correctness fixes (both found by load-testing the export — a naive
convert_float_to_float16() produces a model that CRASHES at load on EVERY provider,
CPU and CUDA alike, so it would silently waste a paid GPU run):

  1. FILE-BASED shape inference (onnx.shape_inference.infer_shapes_path). The
     in-memory infer_shapes serializes to a single protobuf and FAILS on the
     ~2.5 GB encoder (2 GB message limit); the path variant operates on disk and
     handles any size. Without populated shapes the converter mis-places Cast
     nodes at fp16/fp32 boundaries.

  2. Keep the INTEGER-DERIVED "length island" in fp32 (node_block_list). The
     encoder takes an int64 `length` input and computes the sub-sampled output
     length with a small scalar chain (Cast→Add→Div→Floor…). The converter turns
     those float CONSTANTS to fp16 but leaves the int→float Cast at fp32, so the
     Add ends up float+float16 — an invalid graph ("/pre_encode/Add bound to
     different types"). We detect every node reachable from an int input but NOT
     from a float input and keep it fp32. That math is zero-FLOP scalar work (no
     speed cost) and fp16 cannot even represent lengths >2048 exactly, so fp32 is
     also the numerically correct choice. Validated: fixed export decodes 180 s of
     real AMI audio bit-for-bit identically to fp32 (0.000% word divergence).

Idempotent: skips a target that already exists. External weights are written
alongside (fp16 ~halves the ~2.5 GB encoder, but we save external regardless so
the >2 GB protobuf limit is never a concern).

Usage:
  python3 scripts/convert_parakeet_fp16.py <fp32_dir> <fp16_dir>
"""

import os
import shutil
import sys
import tempfile
import warnings


def _block_list() -> list[str]:
    raw = os.getenv("PARAKEET_FP16_BLOCK", "LayerNormalization,Softmax")
    return [op.strip() for op in raw.split(",") if op.strip()]


def _fp16_roles() -> list[str]:
    """Which transducer components actually get fp16 compute (the rest are copied
    through as fp32 under the .fp16.onnx name, which is just a filename contract —
    ORT reads the graph's real dtypes).

    Default is ENCODER ONLY. The encoder is ~95+% of the FLOPs and essentially all
    of the weight VRAM (~2.5 GB fp32 → ~1.2 GB fp16), so it carries the whole
    speed/VRAM win. The decoder+joiner are tiny per-call but run ONCE PER EMITTED
    TOKEN in the autoregressive TDT decode loop — measured (BOTTLENECK.md fifteenth
    pass): full-fp16 derails one AMI meeting (ES2002a) to 188% WER on real CUDA
    kernels while the CPU EP (which upcasts to fp32) hides it. Keeping the decode
    loop fp32 removes the error-accumulation path at ~zero FLOP cost.
    Override via PARAKEET_FP16_ROLES=encoder,decoder,joiner for the full-fp16
    diagnostic."""
    raw = os.getenv("PARAKEET_FP16_ROLES", "encoder")
    return [r.strip() for r in raw.split(",") if r.strip()]


def _int_derived_island(graph) -> list[str]:
    """Names of nodes reachable from an INTEGER graph input but not from any FLOAT
    graph input — i.e. pure sequence-length/shape arithmetic that must stay fp32.

    Float-math derived from an int input (the int→float Cast for length division)
    is exactly where convert_float_to_float16 leaves a half-converted, unloadable
    boundary. Keeping the island in fp32 is free (scalar work) and correct."""
    import onnx

    int_types = {onnx.TensorProto.INT64, onnx.TensorProto.INT32}
    float_types = {onnx.TensorProto.FLOAT, onnx.TensorProto.FLOAT16, onnx.TensorProto.DOUBLE}
    int_inputs = [i.name for i in graph.input if i.type.tensor_type.elem_type in int_types]
    float_inputs = [i.name for i in graph.input if i.type.tensor_type.elem_type in float_types]

    def reach(seeds: list[str]) -> set[str]:
        vals = set(seeds)
        nodes: set[str] = set()
        changed = True
        while changed:
            changed = False
            for n in graph.node:
                if n.name in nodes:
                    continue
                if any(i in vals for i in n.input):
                    nodes.add(n.name)
                    vals.update(n.output)
                    changed = True
        return nodes

    return sorted(reach(int_inputs) - reach(float_inputs))


def convert_one(src: str, dst: str, block: list[str]) -> None:
    import onnx
    from onnx.shape_inference import infer_shapes_path
    from onnxconverter_common import float16

    if os.path.exists(dst):
        print(f"  present: {os.path.basename(dst)}")
        return
    src_dir = os.path.dirname(os.path.abspath(src))

    # (1) File-based shape inference — the >2 GB-safe path. The temp file lives
    # beside the source so the inferred model's relative external-data reference
    # (encoder.weights) still resolves when we reload it.
    fd, inferred = tempfile.mkstemp(dir=src_dir, suffix=".infer.onnx")
    os.close(fd)
    try:
        infer_shapes_path(src, inferred)
        model = onnx.load(inferred, load_external_data=True)
    finally:
        if os.path.exists(inferred):
            os.remove(inferred)

    # (2) Keep the integer-derived length island in fp32.
    island = _int_derived_island(model.graph)
    print(f"  converting {os.path.basename(src)} → {os.path.basename(dst)} "
          f"(keep_io_types, op_block={block}, fp32 length-island={len(island)} nodes) …")

    with warnings.catch_warnings():
        # The converter warns once per subnormal constant it clamps to the fp16
        # floor (1e-7) — thousands of benign lines. Silence them.
        warnings.simplefilter("ignore")
        fp16_model = float16.convert_float_to_float16(
            model,
            keep_io_types=True,
            disable_shape_infer=True,  # already inferred above, on disk
            op_block_list=block,
            node_block_list=island,
        )
    # Drop stale intermediate value_info: shape inference stamped fp32 types that
    # the conversion didn't all update, which trips ORT's load-time type check.
    # value_info is optional metadata (ORT re-infers at load); graph.input/output
    # — the real IO contract — are untouched.
    del fp16_model.graph.value_info[:]

    weights = os.path.basename(dst) + ".weights"
    # Drop any stale external-data file first so save_model writes fresh.
    wpath = os.path.join(os.path.dirname(dst), weights)
    if os.path.exists(wpath):
        os.remove(wpath)
    onnx.save_model(
        fp16_model,
        dst,
        save_as_external_data=True,
        all_tensors_to_one_file=True,
        location=weights,
        size_threshold=1024,
        convert_attribute=False,
    )
    print(f"  wrote {os.path.basename(dst)} (+ {weights})")


def main() -> int:
    if len(sys.argv) != 3:
        print(__doc__)
        return 2
    src_dir, dst_dir = sys.argv[1], sys.argv[2]

    roles = ["encoder", "decoder", "joiner"]
    srcs = {}
    for r in roles:
        # Prefer the plain fp32 file; fall back to any role*.onnx that is not a
        # quantized/half variant.
        cand = os.path.join(src_dir, f"{r}.onnx")
        if not os.path.exists(cand):
            print(f"ERROR: fp32 {r} not found at {cand} "
                  f"(run fetch-bench-deps.sh with BENCH_STT_PRECISION=fp32 first)")
            return 1
        srcs[r] = cand

    tokens = os.path.join(src_dir, "tokens.txt")
    if not os.path.exists(tokens):
        print(f"ERROR: tokens.txt not found at {tokens}")
        return 1

    os.makedirs(dst_dir, exist_ok=True)
    block = _block_list()
    fp16_roles = _fp16_roles()
    print(f"==> Parakeet fp32 → fp16 ({src_dir} → {dst_dir}; fp16 roles: {','.join(fp16_roles)})")
    for r in roles:
        dst = os.path.join(dst_dir, f"{r}.fp16.onnx")
        if r in fp16_roles:
            convert_one(srcs[r], dst, block)
            continue
        # Copy-through: this component stays fp32 (see _fp16_roles). The fp32
        # encoder keeps weights in an external file the graph references by name,
        # so bring that along too if present.
        if not os.path.exists(dst):
            shutil.copyfile(srcs[r], dst)
            ext = srcs[r].replace(".onnx", ".weights")
            if os.path.exists(ext):
                shutil.copyfile(ext, os.path.join(dst_dir, os.path.basename(ext)))
            print(f"  copied {r}.onnx → {os.path.basename(dst)} (fp32 passthrough)")
        else:
            print(f"  present: {os.path.basename(dst)}")

    dst_tokens = os.path.join(dst_dir, "tokens.txt")
    if not os.path.exists(dst_tokens):
        shutil.copyfile(tokens, dst_tokens)
        print("  copied tokens.txt")
    print("==> fp16 conversion done")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
