package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
)

const (
	docxImageURLMapEntry       = "word/_fbif_imageUrls.json"
	maxDocMirrorRawBytes int64 = 30 << 20
)

var docImageMirrorSlots = make(chan struct{}, 2)

func docImageMirroringConfigured() bool {
	return strings.TrimSpace(os.Getenv("WECHAT_UPLOAD_ENDPOINT")) != ""
}

func readDocxImageURLMap(docxBytes []byte) (map[string]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(docxBytes), int64(len(docxBytes)))
	if err != nil {
		return nil, fmt.Errorf("open optimized docx: %w", err)
	}
	for _, file := range zr.File {
		if file.Name != docxImageURLMapEntry {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open image URL map: %w", err)
		}
		data, readErr := readBoundedBody(rc, 1<<20)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read image URL map: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close image URL map: %w", closeErr)
		}
		var imageURLs map[string]string
		if err := json.Unmarshal(data, &imageURLs); err != nil {
			return nil, fmt.Errorf("decode image URL map: %w", err)
		}
		return imageURLs, nil
	}
	return nil, errors.New("optimized docx is missing image URL map")
}

func replaceDocxImageURLMap(docxBytes []byte, imageURLs map[string]string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(docxBytes), int64(len(docxBytes)))
	if err != nil {
		return nil, fmt.Errorf("open optimized docx: %w", err)
	}
	encoded, err := json.Marshal(imageURLs)
	if err != nil {
		return nil, fmt.Errorf("encode mirrored image URLs: %w", err)
	}

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	replaced := false
	for _, file := range zr.File {
		header := file.FileHeader
		writer, err := zw.CreateHeader(&header)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", file.Name, err)
		}
		if file.Name == docxImageURLMapEntry {
			if _, err := writer.Write(encoded); err != nil {
				return nil, fmt.Errorf("write mirrored image URLs: %w", err)
			}
			replaced = true
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", file.Name, err)
		}
		_, copyErr := copyBounded(writer, rc, maxDocxEntryBytes)
		closeErr := rc.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("copy %s: %w", file.Name, copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close %s: %w", file.Name, closeErr)
		}
	}
	if !replaced {
		return nil, errors.New("optimized docx is missing image URL map")
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close optimized docx: %w", err)
	}
	return out.Bytes(), nil
}

func validMirroredImageURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

// mirrorCachedDocxImagesToOSS sends the already-extracted image bytes directly
// to the existing upload service. This avoids the old public-URL loop where the
// upload service had to download every /api/doc-cache image through the slow
// browser-facing route before it could write OSS.
func mirrorCachedDocxImagesToOSS(ctx context.Context, originalDocx, optimizedDocx []byte) ([]byte, int, error) {
	imageURLs, err := readDocxImageURLMap(optimizedDocx)
	if err != nil {
		return nil, 0, err
	}
	if len(imageURLs) == 0 {
		reportFeishuExportProgress(ctx, "image_enumerate", 0, 0, "images")
		return optimizedDocx, 0, nil
	}

	filenames := make([]string, 0, len(imageURLs))
	for filename := range imageURLs {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)

	sum := sha256.Sum256(originalDocx)
	hash := hex.EncodeToString(sum[:])[:16]
	if cached, ok := docCacheMirroredURLs(hash); ok && len(cached) == len(imageURLs) {
		complete := true
		for filename := range imageURLs {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			if !validMirroredImageURL(cached[filename]) {
				complete = false
				break
			}
		}
		if complete {
			reportFeishuExportProgress(ctx, "image_enumerate", 0, int64(len(filenames)), "images")
			reportFeishuExportProgress(ctx, "image_enumerate", int64(len(filenames)), int64(len(filenames)), "images")
			reportFeishuExportProgress(ctx, "image_upload", 0, 0, "images")
			reportFeishuExportProgress(ctx, "image_map", 0, 1, "document")
			rewritten, err := replaceDocxImageURLMap(optimizedDocx, cached)
			if err == nil {
				reportFeishuExportProgress(ctx, "image_map", 1, 1, "document")
			}
			return rewritten, len(cached), err
		}
	}

	select {
	case docImageMirrorSlots <- struct{}{}:
		defer func() { <-docImageMirrorSlots }()
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	}

	var rawBytes int64
	base64Images := make(map[string]string, len(filenames))
	keyToFilename := make(map[string]string, len(filenames))
	reportFeishuExportProgress(ctx, "image_enumerate", 0, int64(len(filenames)), "images")
	for index, filename := range filenames {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		data, ok := docCacheGet(hash, filename)
		if !ok {
			return nil, 0, fmt.Errorf("document image cache miss: %s", filename)
		}
		rawBytes += int64(len(data))
		if rawBytes > maxDocMirrorRawBytes {
			return nil, 0, fmt.Errorf("document images exceed direct mirror limit: %d MB", maxDocMirrorRawBytes>>20)
		}
		mime := docImageMimeFromExt(strings.ToLower(path.Ext(filename)))
		if !strings.HasPrefix(mime, "image/") {
			return nil, 0, fmt.Errorf("unsupported document image: %s", filename)
		}
		key := fmt.Sprintf("doc_image_%03d", index)
		keyToFilename[key] = filename
		base64Images[key] = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		reportFeishuExportProgress(ctx, "image_enumerate", int64(index+1), int64(len(filenames)), "images")
	}

	reportFeishuExportProgress(ctx, "image_upload", 0, int64(len(base64Images)), "images")
	results, err := ossUploadByBase64Images(ctx, base64Images)
	if err != nil {
		return nil, 0, err
	}
	reportFeishuExportProgress(ctx, "image_upload", int64(len(base64Images)), int64(len(base64Images)), "images")
	mirrored := make(map[string]string, len(filenames))
	for key, filename := range keyToFilename {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		value := results[key]
		if !validMirroredImageURL(value) {
			return nil, 0, fmt.Errorf("image upload result missing for %s", filename)
		}
		mirrored[filename] = value
	}
	reportFeishuExportProgress(ctx, "image_map", 0, 1, "document")
	rewritten, err := replaceDocxImageURLMap(optimizedDocx, mirrored)
	if err != nil {
		return nil, 0, err
	}
	reportFeishuExportProgress(ctx, "image_map", 1, 1, "document")
	docCacheSetMirroredURLs(hash, mirrored)
	return rewritten, len(mirrored), nil
}
