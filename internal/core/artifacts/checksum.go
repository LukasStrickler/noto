package artifacts

import (
	"crypto/sha256"
	"fmt"
)

const (
	ChecksumPrefix = "sha256:"
	ChecksumAlgo   = "sha256"
)

func ComputeChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return ChecksumPrefix + fmt.Sprintf("%x", hash)
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
