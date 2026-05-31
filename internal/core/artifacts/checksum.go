package artifacts

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
)

const (
	ChecksumPrefix = "sha256:"
	ChecksumAlgo   = "sha256"
)

func ComputeChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return ChecksumPrefix + fmt.Sprintf("%x", hash)
}

func ComputeJSONChecksum(v any) (string, error) {
	canonical, err := CanonicalJSON(v)
	if err != nil {
		return "", err
	}
	return ComputeChecksum(canonical), nil
}

func CanonicalJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	canonical := sortJSON(raw)
	return json.Marshal(canonical)
}

func sortJSON(v any) any {
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sorted := make(map[string]any, len(val))
		for _, k := range keys {
			sorted[k] = sortJSON(val[k])
		}
		return sorted
	case []any:
		result := make([]any, len(val))
		for i, elem := range val {
			result[i] = sortJSON(elem)
		}
		return result
	default:
		return val
	}
}

func ParseChecksum(checksum string) (algo, hash string, err error) {
	if len(checksum) < len(ChecksumPrefix) {
		return "", "", fmt.Errorf("invalid checksum format: %s", checksum)
	}
	if checksum[:len(ChecksumPrefix)] != ChecksumPrefix {
		return "", "", fmt.Errorf("unsupported checksum prefix: %s", checksum[:len(ChecksumPrefix)])
	}
	return ChecksumAlgo, checksum[len(ChecksumPrefix):], nil
}

func VerifyChecksum(data []byte, expected string) error {
	_, expectedHash, err := ParseChecksum(expected)
	if err != nil {
		return err
	}
	actual := sha256.Sum256(data)
	actualHash := fmt.Sprintf("%x", actual)
	if actualHash != expectedHash {
		return NewChecksumError(expected, ChecksumPrefix+actualHash)
	}
	return nil
}
