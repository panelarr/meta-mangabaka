// SPDX-License-Identifier: GPL-3.0-or-later

// Package dump downloads, verifies, decompresses and reads MangaBaka's
// SQLite database dump.
package dump

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // MangaBaka publishes SHA-1 checksums; we verify what they publish
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// DefaultBaseURL is where MangaBaka publishes its dumps.
const DefaultBaseURL = "https://api.mangabaka.org/v1/database"

// Variants of the dump.
const (
	VariantDefault = "default"
	// VariantFull adds the raw upstream responses of every source.
	VariantFull = "full"
)

// ErrChecksumMismatch means the downloaded file does not match the
// published checksum. The file is deleted and never decompressed.
var ErrChecksumMismatch = errors.New("dump: checksum mismatch")

// ProgressFunc reports work in progress: phase is "download", "verify" or
// "decompress"; unit is "bytes".
type ProgressFunc func(phase string, done, total int64)

// Source is where a dump comes from and where it is kept locally.
type Source struct {
	// BaseURL is the directory the dump files live in.
	BaseURL string
	// Variant is VariantDefault or VariantFull.
	Variant string
	// Dir holds downloads and the decompressed database.
	Dir string
	// HTTP is the client for downloads.
	HTTP *http.Client
	// UserAgent identifies the plugin to MangaBaka.
	UserAgent string
	// Progress, if set, receives progress reports.
	Progress ProgressFunc
	// KeepDownloads keeps the compressed file after decompression.
	KeepDownloads bool
}

// Snapshot is a verified, decompressed dump ready to read.
type Snapshot struct {
	// SHA1 is the checksum of the compressed file, as published.
	SHA1 string
	// Path is the decompressed SQLite database.
	Path string
	// Reused is true when an earlier download with the same checksum was
	// used instead of downloading again.
	Reused bool
}

// FileName is the compressed dump's file name for the variant.
func (s *Source) FileName() string {
	if s.Variant == VariantFull {
		return "series.full.sqlite.zst"
	}
	return "series.sqlite.zst"
}

func (s *Source) url(name string) string {
	return strings.TrimRight(s.BaseURL, "/") + "/" + name
}

func (s *Source) progress(phase string, done, total int64) {
	if s.Progress != nil {
		s.Progress(phase, done, total)
	}
}

func (s *Source) get(ctx context.Context, url string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
	return s.HTTP.Do(req)
}

var sha1Pattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// RemoteChecksum fetches the published SHA-1 of the compressed dump.
func (s *Source) RemoteChecksum(ctx context.Context) (string, error) {
	res, err := s.get(ctx, s.url(s.FileName()+".sha1"), nil)
	if err != nil {
		return "", fmt.Errorf("dump: fetch checksum: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dump: fetch checksum: HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("dump: read checksum: %w", err)
	}
	return ParseChecksum(string(body))
}

// ParseChecksum reads a sha1sum-style line: "<hex>  <file name>".
func ParseChecksum(s string) (string, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 || !sha1Pattern.MatchString(fields[0]) {
		return "", fmt.Errorf("dump: malformed checksum file %q", strings.TrimSpace(s))
	}
	return strings.ToLower(fields[0]), nil
}

// DatabasePath is where the decompressed database for a checksum lives.
func (s *Source) DatabasePath(sum string) string {
	base := strings.TrimSuffix(s.FileName(), ".sqlite.zst")
	return filepath.Join(s.Dir, base+"."+sum+".sqlite")
}

// Fetch makes sure the dump with checksum sum is downloaded, verified and
// decompressed, and returns it. An existing database for the same checksum
// is reused. A partial download for the same checksum is resumed.
func (s *Source) Fetch(ctx context.Context, sum string) (*Snapshot, error) {
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("dump: %w", err)
	}
	dbPath := s.DatabasePath(sum)
	if _, err := os.Stat(dbPath); err == nil {
		return &Snapshot{SHA1: sum, Path: dbPath, Reused: true}, nil
	}

	zstPath := filepath.Join(s.Dir, s.FileName()+"."+sum)
	if _, err := os.Stat(zstPath); err != nil {
		if err := s.download(ctx, zstPath+".part"); err != nil {
			return nil, err
		}
		if err := s.verify(ctx, zstPath+".part", sum); err != nil {
			return nil, err
		}
		if err := os.Rename(zstPath+".part", zstPath); err != nil {
			return nil, fmt.Errorf("dump: %w", err)
		}
	}
	if err := s.decompress(ctx, zstPath, dbPath); err != nil {
		return nil, err
	}
	if !s.KeepDownloads {
		_ = os.Remove(zstPath)
	}
	s.cleanup(sum)
	return &Snapshot{SHA1: sum, Path: dbPath}, nil
}

// download fetches the compressed dump into part, resuming from what part
// already holds.
func (s *Source) download(ctx context.Context, part string) error {
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o640) //nolint:gosec // path is built from the configured dir
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	defer func() { _ = f.Close() }()
	have, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}

	header := http.Header{}
	if have > 0 {
		header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	res, err := s.get(ctx, s.url(s.FileName()), header)
	if err != nil {
		return fmt.Errorf("dump: download: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	var total int64
	switch res.StatusCode {
	case http.StatusPartialContent:
		total = have + res.ContentLength
	case http.StatusOK:
		// The server ignored the range: start over.
		if err := f.Truncate(0); err != nil {
			return fmt.Errorf("dump: %w", err)
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("dump: %w", err)
		}
		have = 0
		total = res.ContentLength
	case http.StatusRequestedRangeNotSatisfiable:
		// We already have the whole file (or more than it); the checksum
		// decides.
		return nil
	default:
		return fmt.Errorf("dump: download: HTTP %d", res.StatusCode)
	}

	w := bufio.NewWriterSize(f, 1<<20)
	buf := make([]byte, 256<<10)
	done := have
	s.progress("download", done, total)
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return fmt.Errorf("dump: %w", err)
			}
			done += int64(n)
			s.progress("download", done, total)
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			_ = w.Flush()
			return fmt.Errorf("dump: download interrupted after %d bytes (will resume): %w", done, rerr)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	return f.Sync()
}

// verify checks the SHA-1 of path. On a mismatch it deletes the file.
func (s *Source) verify(ctx context.Context, path, want string) error {
	f, err := os.Open(path) //nolint:gosec // path is built from the configured dir
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	h := sha1.New() //nolint:gosec // see import
	if _, err := io.Copy(h, &progressReader{ctx: ctx, r: f, total: st.Size(), phase: "verify", report: s.progress}); err != nil {
		return fmt.Errorf("dump: verify: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("%w: published %s, downloaded file has %s", ErrChecksumMismatch, want, got)
	}
	return nil
}

func (s *Source) decompress(ctx context.Context, src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // path is built from the configured dir
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	defer func() { _ = in.Close() }()
	st, err := in.Stat()
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	dec, err := zstd.NewReader(&progressReader{ctx: ctx, r: in, total: st.Size(), phase: "decompress", report: s.progress},
		zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(1<<31))
	if err != nil {
		return fmt.Errorf("dump: decompress: %w", err)
	}
	defer dec.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640) //nolint:gosec // path is built from the configured dir
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	if _, err := io.Copy(out, dec); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("dump: decompress: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("dump: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	return nil
}

// cleanup removes databases, downloads and partial files of this variant
// that belong to other checksums.
func (s *Source) cleanup(keep string) {
	base := strings.TrimSuffix(s.FileName(), ".sqlite.zst")
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		var sum string
		switch {
		case strings.HasPrefix(name, base+".") && strings.HasSuffix(name, ".sqlite"):
			sum = strings.TrimSuffix(strings.TrimPrefix(name, base+"."), ".sqlite")
		case strings.HasPrefix(name, s.FileName()+"."):
			sum = strings.TrimSuffix(strings.TrimPrefix(name, s.FileName()+"."), ".part")
		default:
			continue
		}
		if sha1Pattern.MatchString(sum) && sum != keep {
			_ = os.Remove(filepath.Join(s.Dir, name))
		}
	}
}

type progressReader struct {
	ctx    context.Context
	r      io.Reader
	done   int64
	total  int64
	phase  string
	report ProgressFunc
	calls  int
}

func (p *progressReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.r.Read(b)
	p.done += int64(n)
	p.calls++
	if p.report != nil && (p.calls%64 == 0 || errors.Is(err, io.EOF)) {
		p.report(p.phase, p.done, p.total)
	}
	return n, err
}
