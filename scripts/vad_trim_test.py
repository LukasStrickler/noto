#!/usr/bin/env python3
"""$0 unit tests for vad_trim's pure timeline math (no torch, no silero).

Run: python3 scripts/vad_trim_test.py
"""

import unittest

from vad_trim import TimeMap, keep_spans, plan, sample_slices


class TestKeepSpans(unittest.TestCase):
    def test_empty(self):
        self.assertEqual(keep_spans([], 60.0, 0.4, 1.0), [])

    def test_zero_length_regions_dropped(self):
        self.assertEqual(keep_spans([(5.0, 5.0)], 60.0, 0.4, 1.0), [])

    def test_single_region_padded_and_clipped(self):
        self.assertEqual(keep_spans([(0.2, 59.9)], 60.0, 0.4, 1.0), [(0.0, 60.0)])

    def test_short_gap_kept_whole(self):
        # padded: (0.6,2.4) and (3.1,5.4) → gap 0.7 < 1.0 → merged, silence kept
        self.assertEqual(keep_spans([(1.0, 2.0), (3.5, 5.0)], 60.0, 0.4, 1.0), [(0.6, 5.4)])

    def test_long_gap_collapsed(self):
        # padded: (0.6,2.4) and (9.6,11.4) → gap 7.2 ≥ 1.0 → two spans
        self.assertEqual(
            keep_spans([(1.0, 2.0), (10.0, 11.0)], 60.0, 0.4, 1.0),
            [(0.6, 2.4), (9.6, 11.4)],
        )

    def test_unsorted_input(self):
        self.assertEqual(
            keep_spans([(10.0, 11.0), (1.0, 2.0)], 60.0, 0.4, 1.0),
            [(0.6, 2.4), (9.6, 11.4)],
        )

    def test_overlapping_regions_merge(self):
        self.assertEqual(keep_spans([(1.0, 4.0), (3.0, 6.0)], 60.0, 0.0, 1.0), [(1.0, 6.0)])


class TestSampleSlices(unittest.TestCase):
    def test_quantize_and_clip(self):
        self.assertEqual(
            sample_slices([(0.5, 1.0), (2.0, 99.0)], 16000, 16000 * 3),
            [(8000, 16000), (32000, 48000)],
        )

    def test_monotonic_no_overlap(self):
        # spans that quantize to touching ranges must not overlap
        sl = sample_slices([(0.0, 1.0), (1.0, 2.0)], 16000, 16000 * 2)
        self.assertEqual(sl, [(0, 16000), (16000, 32000)])

    def test_empty_slice_dropped(self):
        self.assertEqual(sample_slices([(1.0, 1.00001)], 16000, 16000 * 2), [])


class TestTimeMap(unittest.TestCase):
    def test_identity_single_span_from_zero(self):
        m = TimeMap([(0.0, 10.0)])
        self.assertAlmostEqual(m.to_orig(3.7), 3.7)
        self.assertAlmostEqual(m.kept, 10.0)

    def test_two_spans_offsets(self):
        # kept: [2,4) and [10,13) → condensed 0-2 maps to 2-4, 2-5 maps to 10-13
        m = TimeMap([(2.0, 4.0), (10.0, 13.0)])
        self.assertAlmostEqual(m.to_orig(0.0), 2.0)
        self.assertAlmostEqual(m.to_orig(1.5), 3.5)
        self.assertAlmostEqual(m.to_orig(2.0), 10.0)  # splice → later span's start
        self.assertAlmostEqual(m.to_orig(4.9), 12.9)
        self.assertAlmostEqual(m.kept, 5.0)

    def test_clamps_beyond_end(self):
        m = TimeMap([(2.0, 4.0)])
        self.assertAlmostEqual(m.to_orig(99.0), 4.0)

    def test_negative_clamps_to_first_start(self):
        m = TimeMap([(2.0, 4.0)])
        self.assertAlmostEqual(m.to_orig(-1.0), 2.0)

    def test_interval_spanning_splice_covers_gap(self):
        # endpoint remap of a splice-bridging turn claims the dropped gap —
        # which is exactly why turns must use intervals(), not to_orig()
        m = TimeMap([(2.0, 4.0), (10.0, 13.0)])
        start, end = m.to_orig(1.0), m.to_orig(3.0)
        self.assertAlmostEqual(start, 3.0)
        self.assertAlmostEqual(end, 11.0)

    def test_intervals_clip_out_dropped_gap(self):
        # the same bridging turn via intervals(): two pieces, gap excluded
        m = TimeMap([(2.0, 4.0), (10.0, 13.0)])
        self.assertEqual(m.intervals(1.0, 3.0), [(3.0, 4.0), (10.0, 11.0)])

    def test_intervals_inside_one_span(self):
        m = TimeMap([(2.0, 4.0), (10.0, 13.0)])
        self.assertEqual(m.intervals(0.5, 1.5), [(2.5, 3.5)])
        self.assertEqual(m.intervals(2.5, 4.5), [(10.5, 12.5)])

    def test_intervals_exact_boundaries(self):
        m = TimeMap([(2.0, 4.0), (10.0, 13.0)])
        # ends exactly at the splice → only the first span's piece, no sliver
        self.assertEqual(m.intervals(0.0, 2.0), [(2.0, 4.0)])
        # starts exactly at the splice → only the second span's piece
        self.assertEqual(m.intervals(2.0, 5.0), [(10.0, 13.0)])

    def test_intervals_full_range(self):
        m = TimeMap([(2.0, 4.0), (10.0, 13.0)])
        self.assertEqual(m.intervals(0.0, 5.0), [(2.0, 4.0), (10.0, 13.0)])

    def test_intervals_beyond_end_clamped(self):
        m = TimeMap([(2.0, 4.0)])
        self.assertEqual(m.intervals(1.0, 99.0), [(3.0, 4.0)])


class TestPlan(unittest.TestCase):
    SR = 16000

    def test_no_regions_means_skip(self):
        self.assertIsNone(plan(self.SR * 60, self.SR, [], 0.4, 1.0))

    def test_all_speech_means_skip(self):
        self.assertIsNone(plan(self.SR * 60, self.SR, [(0.0, 60.0)], 0.4, 1.0))

    def test_tiny_saving_means_skip(self):
        # speech 0-59.2 padded to 59.6 → only 0.4 s dropped → not worth the copy
        self.assertIsNone(plan(self.SR * 60, self.SR, [(0.0, 59.2)], 0.4, 1.0))

    def test_real_trim_round_trips(self):
        # speech at 0-10 and 50-60 of a 60 s file → middle 40 s mostly dropped
        p = plan(self.SR * 60, self.SR, [(0.0, 10.0), (50.0, 60.0)], 0.4, 1.0)
        self.assertIsNotNone(p)
        slices, tmap = p
        self.assertEqual(slices, [(0, int(10.4 * self.SR)), (int(49.6 * self.SR), 60 * self.SR)])
        kept = sum(b - a for a, b in slices) / self.SR
        self.assertAlmostEqual(tmap.kept, kept, places=6)
        # a word at condensed 10.4+1.0 lands at original 49.6+1.0
        self.assertAlmostEqual(tmap.to_orig(11.4), 50.6, places=6)

    def test_zero_inputs_skip(self):
        self.assertIsNone(plan(0, self.SR, [(0.0, 1.0)], 0.4, 1.0))
        self.assertIsNone(plan(self.SR, 0, [(0.0, 1.0)], 0.4, 1.0))


if __name__ == "__main__":
    unittest.main()
