package core

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxCoreSize = 128 << 20

type Download struct {
	URL      string `json:"url"`
	Checksum string `json:"checksum"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	Tag      string `json:"tag"`
}
type Info struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
}
type Progress struct {
	Stage      string
	Downloaded int64
	Total      int64
}

type progressWriter struct {
	downloaded, total int64
	report            func(Progress)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.downloaded += int64(len(b))
	p.report(Progress{Stage: "downloading", Downloaded: p.downloaded, Total: p.total})
	return len(b), nil
}

func (r *Runtime) Info() Info {
	r.binaryMu.RLock()
	defer r.binaryMu.RUnlock()
	p := r.Binary()
	info := Info{}
	if ValidateBinary(p) != nil {
		return info
	}
	info.Installed = true
	b, _ := os.ReadFile(p + ".version")
	info.Version = strings.TrimSpace(string(b))
	if info.Version == "" {
		info.Version = "bundled"
	}
	f, err := os.Open(p)
	if err == nil {
		defer f.Close()
		h := sha256.New()
		_, _ = io.Copy(h, f)
		info.SHA256 = hex.EncodeToString(h.Sum(nil))
	}
	return info
}
func (r *Runtime) Install(ctx context.Context, d Download) error {
	return r.InstallWithProgress(ctx, d, nil)
}
func (r *Runtime) InstallWithProgress(ctx context.Context, d Download, report func(Progress)) error {
	if report == nil {
		report = func(Progress) {}
	}
	return r.install(func() error {
		expected := strings.ToLower(strings.TrimPrefix(d.Checksum, "sha256:"))
		if len(expected) != 64 {
			return errors.New("下载文件缺少 SHA-256 校验和，请为发布文件补充校验和或在打包时内置内核")
		}
		if _, err := hex.DecodeString(expected); err != nil {
			return errors.New("无效 SHA-256 校验和")
		}
		secureURL := func(raw string) error {
			u, err := url.Parse(raw)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return errors.New("内核下载地址必须使用 HTTPS")
			}
			return nil
		}
		if err := secureURL(d.URL); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(r.binary), 0o700); err != nil {
			return err
		}
		archive, err := os.CreateTemp(filepath.Dir(r.binary), "download-*")
		if err != nil {
			return err
		}
		defer os.Remove(archive.Name())
		defer archive.Close()
		req, err := http.NewRequestWithContext(ctx, "GET", d.URL, nil)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("过多下载重定向")
			}
			return secureURL(req.URL.String())
		}}
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return fmt.Errorf("下载失败（HTTP %d）", res.StatusCode)
		}
		total := d.Size
		if total <= 0 {
			total = res.ContentLength
		}
		if total < 0 {
			total = 0
		}
		progress := &progressWriter{total: total, report: report}
		report(Progress{Stage: "downloading", Total: total})
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(archive, h, progress), io.LimitReader(res.Body, maxCoreSize+1))
		if err != nil {
			return err
		}
		if n > maxCoreSize {
			return errors.New("内核压缩包过大")
		}
		if d.Size > 0 && n != d.Size {
			return errors.New("内核文件大小不匹配")
		}
		if hex.EncodeToString(h.Sum(nil)) != expected {
			return errors.New("内核 SHA-256 校验失败")
		}
		report(Progress{Stage: "verifying", Downloaded: n, Total: total})
		if _, err = archive.Seek(0, 0); err != nil {
			return err
		}
		candidate, err := os.CreateTemp(filepath.Dir(r.binary), "frpc-*")
		if err != nil {
			return err
		}
		defer os.Remove(candidate.Name())
		defer candidate.Close()
		if err = extract(archive, d.Filename, candidate); err != nil {
			return err
		}
		if err = candidate.Sync(); err != nil {
			return err
		}
		if err = candidate.Close(); err != nil {
			return err
		}
		if err = ValidateBinary(candidate.Name()); err != nil {
			return err
		}
		if err = os.Chmod(candidate.Name(), 0o700); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = os.Rename(candidate.Name(), r.binary); err != nil {
			return err
		}
		return os.WriteFile(r.binary+".version", []byte(d.Tag), 0o600)
	})
}
func extract(archive *os.File, name string, out io.Writer) error {
	copyFile := func(reader io.Reader) error {
		n, err := io.Copy(out, io.LimitReader(reader, maxCoreSize+1))
		if n > maxCoreSize {
			return errors.New("解压后的内核过大")
		}
		return err
	}
	accepted := func(name string) bool {
		base := strings.ToLower(filepath.Base(name))
		return base == "frpc" || base == "loliafrp-cli" || base == "loliafrp"
	}
	if strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz") {
		gz, err := gzip.NewReader(archive)
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(io.LimitReader(gz, 2*maxCoreSize))
		for count := 0; count < 1000; count++ {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if header.Typeflag == tar.TypeReg && accepted(header.Name) {
				return copyFile(tr)
			}
		}
	} else if strings.HasSuffix(name, ".zip") {
		st, err := archive.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(archive, st.Size())
		if err != nil {
			return err
		}
		for _, file := range zr.File {
			if file.Mode().IsRegular() && accepted(file.Name) {
				reader, err := file.Open()
				if err != nil {
					return err
				}
				defer reader.Close()
				return copyFile(reader)
			}
		}
	} else {
		return copyFile(archive)
	}
	return errors.New("压缩包中找不到 frpc / loliafrp-cli 可执行文件")
}
