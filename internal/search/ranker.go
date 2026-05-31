package search

const (
	BM25K1 = 1.2
	BM25B  = 0.75
)

type BM25Config struct {
	K1 float64
	B  float64
}

func DefaultBM25Config() BM25Config {
	return BM25Config{
		K1: BM25K1,
		B:  BM25B,
	}
}
