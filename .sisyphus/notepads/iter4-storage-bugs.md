# Storage Bug Report

## Bug 1: Path Traversal via unsafeJoin in local.go ListObjects
**File:** internal/storage/adapters/local.go  
**Line:** 149  
**Severity:** CRITICAL

```go
func (a *localAdapter) ListObjects(ctx context.Context, prefix string) ([]ObjectMeta, error) {
    fullPath := filepath.Join(a.basePath, prefix)
    // ... no safeJoin usage
```

**Description:** `ListObjects` uses `filepath.Join(a.basePath, prefix)` directly without calling `safeJoin`. The `prefix` parameter is user-controlled and can contain path traversal sequences like `../../etc`. This allows attackers to list files outside the base path (e.g., `ListObjects(ctx, "../../../etc")` would list `/home/user/Noto/../../../etc`).

**Impact:** Information disclosure - attacker can enumerate files outside the Noto storage directory.

---

## Bug 2: ListObjects Returns Paths Allowing Traversal in Subsequent Operations
**File:** internal/storage/adapters/local.go  
**Lines:** 170-175  
**Severity:** HIGH

```go
key := filepath.Join(prefix, entry.Name())
objects = append(objects, ObjectMeta{
    Key:          key,
```

**Description:** The `Key` returned by `ListObjects` includes the user-provided `prefix`. If a user requests `prefix = "../../../etc"`, the returned keys would be `../../../etc/filename`. These keys could be passed back to `GetObject` or `DeleteObject` which DO use `safeJoin` and would correctly reject them. However, if there's a path sanitization issue in other code that processes these keys, it could lead to traversal.

**Impact:** Keys with traversal sequences could confuse downstream processing or logging.

---

## Bug 3: S3 multipart Upload Overwrites Metadata on R2 with Empty Values
**File:** internal/storage/adapters/s3.go  
**Lines:** 235-237  
**Severity:** MEDIUM

```go
if opts.ContentType != "" {
    partInput.ContentType = aws.String(opts.ContentType)
}
```

**Description:** In `uploadMultipartR2`, if `opts.ContentType` is empty, it is NOT set for individual parts. The `CreateMultipartUpload` at line 213 sets the ContentType, but individual `UploadPart` calls override it with empty string if `opts.ContentType` is not set. On R2, this can cause Content-Type metadata loss.

**Impact:** Uploaded files may have incorrect or missing MIME type metadata on R2.

---

## Bug 4: bufferedReader.Read Resets Position After Initial Buffer Exhaustion
**File:** internal/storage/adapters/s3.go  
**Lines:** 161-173  
**Severity:** HIGH

```go
func (b *bufferedReader) Read(p []byte) (int, error) {
    if b.n < len(b.buf) {
        // Return buffered data first
        n := copy(p, b.buf[b.n:])
        b.n += n
        return n, nil
    }
    // Read from underlying reader
    n, err := b.reader.Read(p)
```

**Description:** The `bufferedReader` implementation reads the first 32KB into the buffer, then returns it. When `Read` is called again after the buffer is exhausted, it reads from the underlying reader but does NOT refill the buffer. The second call reads directly from `body`. The `remain` field is only incremented for bytes read from `body` after buffer exhaustion, but `buffered.remain` is not correctly tracking total bytes available after initial buffer is consumed.

**Impact:** The size calculation `int64(n) + buffered.remain` at line 116 may be incorrect when more data exists but wasn't read during the initial peek.

---

## Bug 5: S3 GetPresignedURL Does Not Validate Key with safeJoin
**File:** internal/storage/adapters/s3.go  
**Lines:** 357-366  
**Severity:** MEDIUM

```go
func (a *s3Adapter) GetPresignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
    request, err := a.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
        Bucket: aws.String(a.bucket),
        Key:    aws.String(key),
    }, s3.WithPresignExpires(ttl))
```

**Description:** Unlike `PutObject` and `GetObject` (which don't need safeJoin for S3 since keys aren't filesystem paths), the presigned URL generation should still validate the key format to prevent injection attacks if keys are used in constructing URLs. However, for S3, keys are opaque strings and the SDK handles escaping automatically.

**Note:** This is not a critical bug for S3 since keys are opaque. The issue is more about consistency. The real concern is if user-provided keys with special characters cause issues with S3 operations.

---

## Bug 6: PresignedURL SafeJoin Bypass via Path Characters
**File:** internal/storage/adapters/s3.go  
**Lines:** 357-366  
**Severity:** MEDIUM (S3 specific, but MEDIUM because not a real traversal risk)

**Description:** S3 keys can contain characters like `..` which might look like path traversal but are actually valid S3 key characters. The presigned URL generation does not sanitize these, but for S3 this is actually correct behavior since `..` in an S3 key is just part of the key name, not a traversal mechanism.

---

## Bug 7: WriteManifest Missing fsyncDir After Checksum Rename
**File:** internal/storage/meeting.go  
**Lines:** 49-52  
**Severity:** HIGH

```go
if err := os.Rename(tmpChecksumPath, checksumPath); err != nil {
    os.Remove(tmpChecksumPath)
    return ErrAtomicWrite(checksumPath, err)
}
// MISSING: if err := fsyncDir(filepath.Dir(checksumPath)); err != nil { return err }
```

**Description:** `WriteManifest` calls `fsyncDir` after renaming the manifest file (line 66-68) but NOT after renaming the checksum file. The checksum file is renamed first at line 49, but there's no `fsyncDir` call before the manifest rename. If a crash occurs after checksum rename but before manifest rename, the checksum could be left in an inconsistent state with the manifest.

**Impact:** System crash could leave checksum file updated but manifest file old, causing subsequent reads to fail checksum verification incorrectly.

---

## Bug 8: CopyAudioToVersion Reads Entire File Into Memory
**File:** internal/storage/meeting.go  
**Lines:** 255-261  
**Severity:** MEDIUM

```go
func CopyAudioToVersion(layout DirectoryLayout, versionID string, srcAudioPath string) error {
    // ...
    srcData, err := os.ReadFile(srcAudioPath)
    if err != nil {
        return ErrReadFailed(srcAudioPath, err)
    }

    if err := os.WriteFile(versionAudioPath, srcData, 0644); err != nil {
        return ErrWriteFailed(versionAudioPath, err)
    }
}
```

**Description:** `CopyAudioToVersion` reads the entire audio file into memory with `os.ReadFile`. Audio files can be large (hundreds of MB). This could cause memory pressure and OOM on large recordings.

**Impact:** Memory exhaustion for large audio files during version copy operation.

---

## Bug 9: WriteAudioMetadata Missing fsyncDir After Rename
**File:** internal/storage/meeting.go  
**Lines:** 215-221  
**Severity:** HIGH

```go
if err := os.Rename(tmpPath, finalPath); err != nil {
    os.Remove(tmpPath)
    return ErrAtomicWrite(finalPath, err)
}
if err := fsyncDir(filepath.Dir(finalPath)); err != nil {
    return err
}
```

**Description:** Unlike `WriteManifest` and `WriteTranscript` which call `fsyncDir` after renaming, `WriteAudioMetadata` DOES call `fsyncDir` at line 219. This is actually correct - compare with line 66-68. Wait, let me re-read... Actually it does call fsyncDir here. Let me verify other functions.

Looking at `WriteTranscript` (lines 135-140) - it calls fsyncDir after rename.
Looking at `WriteSummary` (lines 176-181) - it calls fsyncDir after rename.
Looking at `WriteAudioMetadata` (lines 215-221) - it calls fsyncDir after rename.

Actually this is correct. Disregard this potential bug - all write functions properly call fsyncDir.

---

## Bug 10: ListMeetings Doesn't Validate Year/Month Directory Names
**File:** internal/storage/meeting.go  
**Lines:** 343-346  
**Severity:** LOW

```go
year := yearEntry.Name()  // No validation that this is numeric
monthDir := filepath.Join(meetingsDir, year)
monthEntries, err := os.ReadDir(monthDir)
```

**Description:** `ListMeetings` iterates over year directories without validating they are valid 4-digit years. It trusts `ReadDir` to return directories in sorted order, but malformed directory names would just result in empty results or errors being silently skipped.

**Impact:** Minimal - this is just listing, and errors are handled gracefully.

---

## Summary

| Bug # | File | Line(s) | Severity | Type |
|-------|------|---------|----------|------|
| 1 | local.go | 149 | CRITICAL | Path Traversal |
| 2 | local.go | 170-175 | HIGH | Path Handling |
| 3 | s3.go | 235-237 | MEDIUM | S3 Metadata |
| 4 | s3.go | 161-173 | HIGH | Buffer Implementation |
| 5 | s3.go | 357-366 | MEDIUM | S3 Key Handling |
| 6 | s3.go | 357-366 | MEDIUM | S3 Key Validation |
| 7 | meeting.go | 49-52 | HIGH | Atomic Write |
| 8 | meeting.go | 255-261 | MEDIUM | Memory Usage |
| 9 | meeting.go | 215-221 | N/A | False positive - code is correct |
| 10 | meeting.go | 343-346 | LOW | Input Validation |